# ISR Spec

## Purpose

- Define how public page caching, revalidation, and missing-content handling must behave for correctness and cost control.

## Rules

- Public page delivery MUST use ISR with stale-while-revalidate behavior.
- Revalidation behavior MUST be configured explicitly in the Next.js App Router for relevant pages or fetches.
- On a cold or expired path, the backend MUST be fetched exactly once for the rebuild of that path.
- Warm cached requests MUST be served from cache without requiring a backend fetch.
- Stale cached requests MUST be served immediately while background rebuild occurs.
- Content changes that remain publicly resolvable after the mutation MUST rebuild and recache the path after revalidation.
- For unpublish or delete, the page rebuild for the affected path MUST call `notFound()` when the content no longer exists.
- The system MUST trigger revalidation for delete and unpublish events, not only publish and update events.
- A cached page for removed content MUST NOT continue returning `200` after a successful revalidation cycle.
- Tenant-wide cache tag revalidation MUST be supported for bulk edits, schema migrations, CDN URL changes, and incident recovery.

## Failure Modes

- Missing explicit revalidation configuration can cause readers to wait on rebuilds or amplify cold-path spikes after deploy.
- Missing webhook on delete or unpublish leaves stale pages publicly accessible until TTL expiry.
- Missing `notFound()` on rebuild prevents Vercel from dropping a route that should no longer exist.
- Tenant-wide invalidation gaps make bulk content recovery slow or manual.

## Observability

- Measure cache hit, stale, and rebuild events at the frontend layer.
- Track backend fetch counts for cold and expired paths to confirm one upstream fetch per rebuild.
- Log every revalidation request with target path or tenant cache tag and resulting frontend status.
- Detect routes that continue returning `200` after delete or unpublish events that were marked revalidated.
