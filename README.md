# EzCMS Local Stack

This repo now contains a parity-first local build for the core EzCMS architecture:

- Go API with request-scoped tenancy and PostgreSQL RLS
- Next.js App Router frontend with ISR and revalidation endpoint
- Postgres in Docker for local multi-tenant data
- MinIO in Docker as the local S3-compatible stand-in for Cloudflare R2
- Dead-letter persistence for failed frontend revalidation

## Build Priority

1. Tenancy and RLS correctness
2. Publish, unpublish, delete, and post-commit revalidation
3. ISR not-found behavior for removed content
4. Dead-letter persistence and manual retry
5. Media preprocessing and direct object delivery

## Local Requirements

- Docker Desktop
- Go 1.25+
- Node 23+
- npm 10+

## Boot the Stack

1. Create local env files.

```bash
cp .env.example .env
cp web/.env.local.example web/.env.local
```

2. Install application dependencies.

```bash
make install
```

3. Start local infrastructure.

```bash
make up
```

4. Apply database migrations.

```bash
make migrate
```

5. Start the API.

```bash
make api
```

6. Start the frontend in another terminal.

```bash
make web
```

## Generate a Local Tenant JWT

Generate a token for tenant `acme`:

```bash
node scripts/dev-jwt.mjs acme
```

The script reads `JWT_SECRET` from `.env` if it is exported in your shell. Otherwise it defaults to `local-dev-secret`, which matches the example config.

## Minimal Smoke Flow

1. Create a draft article.

```bash
TOKEN=$(node scripts/dev-jwt.mjs acme)

curl -X POST http://localhost:8080/admin/articles \
  -H "Authorization: Bearer $TOKEN" \
  -H "Content-Type: application/json" \
  -d '{
    "title": "Hello World",
    "slug": "hello-world",
    "body": "This is the first locally published article."
  }'
```

2. Publish the article.

Replace `<ARTICLE_ID>` with the returned article id.

```bash
curl -X POST http://localhost:8080/admin/articles/<ARTICLE_ID>/publish \
  -H "Authorization: Bearer $TOKEN"
```

3. Open the public page.

```text
http://localhost:3000/acme/posts/hello-world
```

4. Unpublish or delete and reload the same URL.

The frontend route should fall through to `notFound()` after revalidation.

## API Endpoints

- `GET /healthz`
- `GET /admin/articles`
- `POST /admin/articles`
- `PUT /admin/articles/:id`
- `POST /admin/articles/:id/publish`
- `POST /admin/articles/:id/unpublish`
- `DELETE /admin/articles/:id`
- `GET /admin/webhook-failures`
- `POST /admin/revalidate`
- `POST /media`
- `GET /public/:tenant/articles/:slug`

## Notes

- Local storage uses MinIO, not real Cloudflare R2. The application uses an S3-compatible codepath so the production swap remains configuration-driven.
- This repo currently uses plain local Postgres rather than full Supabase CLI orchestration. That still proves the critical tenancy and RLS behavior locally without introducing unnecessary platform noise in the first build.
- `make test` sets a workspace-local Go build cache because macOS system cache trimming can fail under sandboxed execution.
The local Postgres container is exposed on host port `5433` to avoid conflicts with any existing service already using `5432`.
