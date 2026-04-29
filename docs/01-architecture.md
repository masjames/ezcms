# EzCMS Architecture

## System Intent Mapping

- Multi-tenant safety -> tenant isolation is enforced by PostgreSQL RLS using request-scoped tenant context.
- Low marginal read cost -> public pages use Next.js ISR and images are served directly from CDN URLs.
- Publish correctness -> cache revalidation happens only after database commit and must cover publish, update, unpublish, and delete.
- Operational resilience -> webhook failures are retried, persisted in PostgreSQL, and recoverable from the admin UI.
- Runtime portability -> the backend is stateless, binds to Render-provided runtime settings, and does not depend on local disk.

---

## 1 · Full System Overview

```
EDITOR SIDE              GO BACKEND · Render Web Service               VERCEL · per-tenant
───────────              ──────────────────────────────────             ───────────────────

┌──────────┐  ①REST     ┌────────────────────────────────────────┐     ┌─────────────────────┐
│ Admin UI │───────────▶│           APPLICATION LAYER            │     │     Next.js         │
│  React   │            │   Handlers · Use Cases · DTOs          │◀────│     ISR             │──▶ READERS
└────┬─────┘            ├────────────────────────────────────────┤  ②  │     stale-while-    │
     │                  │           DOMAIN LAYER                 │     │     revalidate      │
     │                  │   ┌─────────┐  ┌───────────────────┐  │     └──────────┬──────────┘
     │                  │   │ Content │  │      Media        │  │                │
     │                  │   │articles │  │  CDN URLs in DB   │  │     ③ webhook  │
     │                  │   │cat/tags │  │  ← not raw paths  │  │  POST /api/    │
     │                  │   └─────────┘  └───────────────────┘  │  revalidate   │
     │                  │   ┌──────────┐  ┌──────────────────┐  │  ⚠ fire AFTER │
     │                  │   │ Identity │  │    Tenancy ★     │  │    tx commit   │
     │                  │   │ JWT/auth │  │  RLS ctx/request │  │                │
     │                  │   └──────────┘  └──────────────────┘  │◀───────────────┘
     │                  ├────────────────────────────────────────┤
     │  ④ multipart     │           INFRA LAYER                  │
     └─────────────────▶│   PG repo   R2 client   Render runtime │
                        └────────────┬───────────────────┬───────┘
                                     │                   │ pre-processed on upload
                                     ▼                   ▼
                             ┌─────────────┐    ┌─────────────────────┐
                             │ PostgreSQL  │    │   Cloudflare R2     │
                             │ Supabase    │    │   hero  · 1600px    │
                             │ RLS enforced│    │   thumb ·  400px    │
                             └─────────────┘    │   og    · 1200px    │ ← ⑤ cdn.ezcms.com/...
                                                └──────────┬──────────┘       no transform
                                                           │                  no backend
                                                           └──────────────────────────────▶ READERS

 Render contract:
   - web service is stateless; no local file persistence between deploys/restarts
   - binary must bind to $PORT
   - health endpoint required for rollout/readiness
   - graceful SIGTERM handling required for zero-downtime deploys

 ★ Tenancy is the most critical bounded context — see Diagram 4
```

---

## 2 · ISR and Webhook Cache Flow

```
READ PATH · COLD CACHE
══════════════════════════════════════════════════════════════════════════════════

  READER           VERCEL CDN                NEXT.JS (ISR)            GO BACKEND
  ──────           ──────────                ─────────────            ──────────

  GET /posts/x ──▶ hit? ──YES───────────────────────────────────────────────────▶ 200 ✓
                    │            served from CDN edge, backend never touched
                   NO
                    │
                    ▼
              forward request
                    │
                    ▼
            page render/fetch ─────────────────────────────────────▶ fetch data
                    │                                                     │
                    │◀────────────────────────────────────── return content
                    │
              render + cache
                    │
  READER ◀──────── 200  ← backend hit exactly ONCE per cold/expired path
                         ten thousand readers = same compute as one

  ⚠ STAMPEDE RISK
  ┌───────────────────────────────────────────────────────────────────────────┐
  │ trigger: deploy flushes all ISR caches simultaneously                    │
  │ result:  N expired paths → N concurrent upstream fetches to Render       │
  │          one small instance = visible latency spike on cold rebuilds     │
  │                                                                           │
  │ fix:     ISR stale-while-revalidate serves stale immediately,            │
  │          rebuilds async in background — no reader ever waits             │
  │          ACTION: confirm App Router revalidation is set explicitly       │
  │                  e.g. export const revalidate = 60  ← in page file       │
  │                  or fetch(..., { next: { revalidate: 60 } })             │
  └───────────────────────────────────────────────────────────────────────────┘


WRITE PATH · PUBLISH / UNPUBLISH / DELETE
══════════════════════════════════════════════════════════════════════════════════

  EDITOR     USE CASE LAYER         DB            WEBHOOK            NEXT.JS
  ──────     ──────────────         ──            ───────            ───────

  publish ──▶ PublishArticle
                   │
                   ▼
              INSERT/UPDATE ──────▶ COMMIT  ──▶ POST /api/revalidate
                                   ↑                    │
                            ⚠ webhook fires             ▼
                              only AFTER commit    revalidatePath(path)
                              never inside tx      page rebuilt + re-cached

  ⚠ INSIDE-TX TRAP (do not do this)
  ┌───────────────────────────────────────────────────────────────────────────┐
  │  WRONG:  BEGIN → INSERT → POST /api/revalidate → COMMIT                  │
  │          if webhook times out: transaction held open, DB conn blocked    │
  │                                                                           │
  │  RIGHT:  BEGIN → INSERT → COMMIT → POST /api/revalidate                  │
  │          webhook failure is now recoverable; DB conn freed immediately   │
  └───────────────────────────────────────────────────────────────────────────┘

  ⚠ DELETE / UNPUBLISH — same webhook must fire
  ┌───────────────────────────────────────────────────────────────────────────┐
  │ if omitted: deleted article stays live at /posts/x until TTL expires     │
  │                                                                           │
  │ fix:  webhook fires on delete/unpublish too                               │
  │       App Router page must handle missing content:                       │
  │         if (!article) notFound()        ← Vercel drops cache on rebuild  │
  └───────────────────────────────────────────────────────────────────────────┘
```

---

## 3 · Image Upload and Delivery Pipeline

```
UPLOAD PASS · single multipart request, Go Infra Layer
══════════════════════════════════════════════════════════════════════════════════

  Admin UI               Go: receive + process                   Cloudflare R2
  ────────               ────────────────────                   ─────────────

  POST /media ──────────▶ 1. resize   → 1600px max
  (multipart)             2. convert  → WebP q75
                          3. strip EXIF metadata
                          │
                          │  ┌──────────────────────────────────────────────────┐
                          │  │  ⚠ VARIANT DECISION — resolve before shipping   │
                          │  │                                                  │
                          │  │  ALL sizes must be generated in this pass:       │
                          │  │    hero    1600px  (article pages)               │
                          │  │    og      1200px  (Open Graph meta tags)        │
                          │  │    thumb    400px  (admin list views)            │
                          │  │                                                  │
                          │  │  if deferred to first-request instead:           │
                          │  │    → runtime transform = Go/Next.js load         │
                          │  │    → defeats zero-marginal-cost goal entirely    │
                          │  │    → Media bounded context must own variants,    │
                          │  │      not delegate them lazily to consumers       │
                          │  └──────────────────────────────────────────────────┘
                          │
                          ├─ store hero  ──────────────────────▶ /media/{id}/hero.webp
                          ├─ store og    ──────────────────────▶ /media/{id}/og.webp
                          ├─ store thumb ──────────────────────▶ /media/{id}/thumb.webp
                          │
                          └─ write CDN URLs to DB  (not file paths!)
                               media.hero_url  = "cdn.ezcms.com/media/{id}/hero.webp"
                               media.og_url    = "cdn.ezcms.com/media/{id}/og.webp"
                               media.thumb_url = "cdn.ezcms.com/media/{id}/thumb.webp"

  ⚠ STORING URLS NOT PATHS — operational consequence
  ┌───────────────────────────────────────────────────────────────────────────┐
  │ R2 bucket renamed?      → DB migration only, zero code change            │
  │ CDN domain swapped?     → DB migration only, zero code change            │
  │ new variant needed?     → upload pass + backfill job, frontend unchanged │
  └───────────────────────────────────────────────────────────────────────────┘


DELIVERY PATH · zero backend involvement
══════════════════════════════════════════════════════════════════════════════════

  READER          Vercel CDN (HTML)                    Cloudflare CDN (images)
  ──────          ─────────────────                    ───────────────────────

  GET /posts/x ──▶ cached HTML
                   contains: <img src="cdn.ezcms.com/media/{id}/hero.webp">
                   contains: <meta property="og:image" content="cdn.ezcms.com/.../og.webp">
                        │
                        │ browser resolves img src independently
                        │
                        └──────────────────────────────▶ GET cdn.ezcms.com/media/...
                                                         served from Cloudflare edge
                                                         Render app  = not involved
                                                         Next.js     = not involved
                                                         cost        = zero marginal
```

---

## 4 · Tenancy Isolation and RLS

```
INBOUND REQUEST LIFECYCLE
══════════════════════════════════════════════════════════════════════════════════

  HTTP request (JWT contains tenant_id)
        │
        ▼
  ┌─────────────────────────────────────────────────────────────────────────┐
  │  MIDDLEWARE · App Layer                                                 │
  │                                                                         │
  │  1. validate JWT signature                                              │
  │  2. extract tenant_id from claims                                       │
  │  3. attach to request context (not a global, scoped to this request)   │
  └──────────────────────────────────┬──────────────────────────────────────┘
                                     │
                                     ▼
  ┌─────────────────────────────────────────────────────────────────────────┐
  │  INFRA LAYER · PG Repo                                                  │
  │                                                                         │
  │  BEGIN TRANSACTION                                                      │
  │    SET LOCAL app.current_tenant_id = '{tenant_id}'                     │
  │    │         ↑                                                          │
  │    │         LOCAL = scoped to this transaction only                   │
  │    │                cleared on COMMIT/ROLLBACK                         │
  │    │                safe with transaction-mode pooling                 │
  │    │                                                                    │
  │    SELECT * FROM articles          ← no WHERE tenant_id in app code    │
  │                                      RLS enforces it at PG level       │
  │  COMMIT                                                                 │
  └──────────────────────────────────┬──────────────────────────────────────┘
                                     │
                                     ▼
  ┌─────────────────────────────────────────────────────────────────────────┐
  │  POSTGRESQL · RLS POLICY                                                │
  │                                                                         │
  │  CREATE POLICY tenant_isolation ON articles                             │
  │    USING (                                                              │
  │      tenant_id = current_setting('app.current_tenant_id')              │
  │    );                                                                   │
  │                                                                         │
  │  enforced at DB level — app logic bug cannot bypass this               │
  │  two tenants can never see each other's rows, by construction          │
  └─────────────────────────────────────────────────────────────────────────┘


POOLER TRAP · Supabase default = transaction-mode PgBouncer
══════════════════════════════════════════════════════════════════════════════════

  connection pool:  [ conn-A ]  [ conn-B ]  [ conn-C ]  ...

  ── SAFE (SET LOCAL, transaction mode) ──────────────────────────────────────

  req-1 ──▶ borrow conn-A
             SET LOCAL tenant_id = 'acme'
             SELECT ...
            COMMIT  ←── SET LOCAL is cleared here
            return conn-A to pool

  req-2 ──▶ borrow conn-A
             current_setting('app.current_tenant_id') = ''   ← clean ✓
             SET LOCAL tenant_id = 'globex'
             SELECT ...

  ── DANGEROUS (SET SESSION, any pool mode) ──────────────────────────────────

  req-1 ──▶ borrow conn-A
             SET SESSION tenant_id = 'acme'
             SELECT ...
            return conn-A to pool (session setting PERSISTS)

  req-2 ──▶ borrow conn-A
             current_setting('app.current_tenant_id') = 'acme'  ← LEAK ✗
             globex tenant now reads acme data

  ⚠ ACTION REQUIRED
  ┌───────────────────────────────────────────────────────────────────────────┐
  │ write a test that:                                                       │
  │   1. fires N concurrent requests across 2 tenants under load            │
  │   2. asserts each request reads current_setting at query time           │
  │   3. asserts no row from tenant-A is returned in tenant-B results       │
  │                                                                           │
  │ run serially first, then under concurrent load — the bug only           │
  │ appears when connections are reused across requests rapidly              │
  └───────────────────────────────────────────────────────────────────────────┘
```

---

## 5 · Webhook Reliability and Dead-Letter Flow

```
PUBLISH → WEBHOOK → REVALIDATE
══════════════════════════════════════════════════════════════════════════════════

  PublishArticle Use Case
        │
        ├─ 1. BEGIN
        ├─ 2. INSERT / UPDATE article
        ├─ 3. COMMIT  ◀── DB conn freed here
        │
        └─ 4. fire webhook (post-commit, never inside tx)
                   │
  │ attempt 1 ──▶ POST /api/revalidate ──▶ 200 ✓ ──▶ done
                   │                                          │
                   │                                        fail
                   │                                          │
                   ├── attempt 2: wait 2s  ──▶ retry ──▶ 200 ✓ ──▶ done
                   ├── attempt 3: wait 4s  ──▶ retry ──▶ fail
                   └── attempt 4: wait 8s  ──▶ retry ──▶ fail
                                                          │
                                                          ▼
                                               write to dead-letter table


DEAD-LETTER TABLE · webhook_failures
══════════════════════════════════════════════════════════════════════════════════

  ┌────────────┬──────────────────┬────────────┬─────────────┬────────────────┐
  │ id         │ page_path        │ tenant_id  │ status      │ last_attempted │
  ├────────────┼──────────────────┼────────────┼─────────────┼────────────────┤
  │ uuid-1     │ /posts/q3-recap  │ acme       │ failed      │ 2024-11-01 ... │
  │ uuid-2     │ /posts/new-hire  │ acme       │ pending     │ 2024-11-01 ... │
  │ uuid-3     │ /about           │ globex     │ retried     │ 2024-11-01 ... │
  │ uuid-4     │ /posts/roadmap   │ globex     │ resolved    │ 2024-11-01 ... │
  └────────────┴──────────────────┴────────────┴─────────────┴────────────────┘

  status values:  pending → failed → retried → resolved
  ← exposed in Admin UI with filter + sort, not just a server log file
     editors self-serve without needing DB console access

  minimum columns actually required:
    id
    tenant_id
    page_path nullable
    cache_tag nullable
    payload_json
    status
    attempt_count
    last_attempted_at
    last_error
    created_at
    resolved_at nullable

  minimum indexes:
    (tenant_id, status, created_at desc)
    (status, last_attempted_at)


ADMIN RE-TRIGGER ENDPOINT
══════════════════════════════════════════════════════════════════════════════════

  POST /admin/revalidate

  single page:    { "path": "/posts/q3-recap" }
                  └─ one targeted revalidation via revalidatePath(path)

  full tenant:    { "tenant_id": "acme" }
                  └─ revalidates tenant cache tag: tenant:acme
                     needed after: schema migrations, bulk content edits,
                                   CDN URL domain change, incident recovery

  ⚠ ADMIN UI REQUIREMENTS FOR DEAD-LETTER
  ┌───────────────────────────────────────────────────────────────────────────┐
  │  show:   status badge per row  (failed = red, pending = amber, etc.)     │
  │  action: "retry" button per row  → calls /admin/revalidate with path     │
  │  action: "retry all failed" for tenant  → bulk flush                     │
  │  filter: by tenant_id, by status, by date range                          │
  │                                                                           │
  │  without this: editors discover stale pages from reader complaints,      │
  │  not from the tool — that is a support burden, not a feature             │
  └───────────────────────────────────────────────────────────────────────────┘


DECISION MAP · what fires the webhook
══════════════════════════════════════════════════════════════════════════════════

  content event          webhook fires?    notFound fallback needed?
  ─────────────────────  ──────────────    ─────────────────────────
  publish article        YES               NO
  update published       YES               NO
  unpublish article      YES  ← ⚠ often   YES  page calls notFound()
  delete article         YES    missed          during revalidation
  update metadata only   YES               NO
  save draft (unpub)     NO                NO

  ⚠ unpublish/delete gap: without webhook + notFound, the ISR-cached page
    at /posts/x continues returning 200 with stale content until TTL expires.
    Vercel only drops a cached route when the App Router rebuild resolves to
    notFound() for that path — the webhook triggers that revalidation.
```

---

## 6 · Render Deployment Contract

```
RUNTIME RULES · required for implementation-level confidence
══════════════════════════════════════════════════════════════════════════════════

  Render Web Service
  ──────────────────

  start command      ./ezcms-api
  bind address       0.0.0.0:$PORT   ← never hardcode 8080/3000
  health endpoint    GET /healthz
  readiness rule     returns 200 only after:
                       1. config loaded
                       2. DB reachable
                       3. R2 client initialized
  shutdown rule      on SIGTERM:
                       1. stop accepting new requests
                       2. allow in-flight requests to finish
                       3. close DB pool cleanly
  filesystem rule    local disk is ephemeral
                     uploads must stream directly to memory/tmp then R2
                     never store canonical media under /var/app or local paths


ENV CONTRACT · explicit variables, no hidden config
══════════════════════════════════════════════════════════════════════════════════

  DATABASE_URL
  JWT_SECRET or JWKS_URL
  R2_ACCOUNT_ID
  R2_ACCESS_KEY_ID
  R2_SECRET_ACCESS_KEY
  R2_BUCKET
  CDN_BASE_URL                  e.g. https://cdn.ezcms.com
  FRONTEND_REVALIDATE_URL       Vercel endpoint
  FRONTEND_REVALIDATE_TOKEN     shared secret
  PORT                          injected by Render

  optional but recommended:
  WEBHOOK_TIMEOUT_MS            default 3000
  WEBHOOK_MAX_ATTEMPTS          default 4
  LOG_LEVEL


DEPLOY / INCIDENT CONSEQUENCES
══════════════════════════════════════════════════════════════════════════════════

  deploy starts new instance → old instance receives SIGTERM
  therefore:
    - no in-memory job queue may be the only source of truth
    - dead-letter retries must survive restarts in PostgreSQL
    - webhook replay worker must be idempotent
```
