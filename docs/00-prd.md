# EzCMS PRD

## Context

- EzCMS is a multi-tenant CMS used by editors to manage content and media for tenant-specific websites.
- Reader-facing delivery is expected to stay fast and low-cost under bursty read traffic.
- Publishing operations must keep public pages and cached content consistent without requiring manual operator intervention.

## Problem

- A shared CMS can leak tenant data if isolation depends on application code alone.
- Public pages can remain stale after publish, unpublish, or delete if cache invalidation is unreliable.
- Media handling can become an ongoing runtime cost if images are transformed on demand instead of once at upload time.
- Operational failures become support incidents if editors cannot detect and recover from stale cache or failed revalidation themselves.

## Goals

- Enforce tenant isolation at the data layer so one tenant cannot read another tenant's rows.
- Keep reader traffic off the backend for cached content so cold or expired paths hit upstream only once per rebuild cycle.
- Revalidate published content after content changes without holding database transactions open.
- Remove deleted or unpublished pages from public delivery promptly after revalidation.
- Deliver media directly from CDN URLs with no backend hop on reader requests.
- Expose failed cache revalidation to editors with retry capability so stale pages are discoverable and recoverable from the product.

## Non-Goals

- Real-time publishing with no cache window at all.
- Runtime image transformation at request time.
- Local filesystem persistence in the backend runtime.
- Relying on server logs or manual database access as the primary operator workflow for stale-page recovery.

## Core Principles

- Isolation by construction: tenant boundaries are enforced in the database, not trusted to handler code.
- Stateless serving: the backend must tolerate restarts, deploy replacement, and horizontal reuse without local state.
- Cache invalidation is a product requirement: publish correctness includes revalidation and deletion behavior, not only database writes.
- Precompute expensive work once: media variants are generated during upload, not by reader traffic.
- Self-serve operations: editors need product-visible failure states and retry controls.

## Key Flows

- Editor publishes or updates content, and the public page is refreshed after the content change is durably saved.
- Editor unpublishes or deletes content, and the previously public page is removed from reader access after cache refresh.
- Editor uploads media once, and all public consumers use ready-to-serve CDN URLs.
- Reader requests public pages, usually from cache, without invoking the backend on warm paths.
- Operators or editors review failed refresh events and retry targeted or tenant-wide revalidation.

## Constraints

- The backend runtime is stateless and must survive restarts without losing operational truth.
- Tenant traffic shares infrastructure, so isolation must remain correct under concurrent load and pooled connections.
- Public content is served through cache layers that may temporarily serve stale data by design.
- Media delivery must scale without introducing per-request transform cost.

## Risks

- Tenant context leakage across pooled database connections can expose one tenant's data to another.
- Revalidation triggered before transaction commit can block database resources and couple publish latency to webhook failure.
- Missing revalidation on delete or unpublish can leave stale public pages live until cache expiry.
- Failed revalidation without durable tracking can create silent content inconsistency.
- Cache flushes after deploy can create cold-path spikes against the backend.

## Success Criteria

- No cross-tenant read is observable in concurrent production-like access patterns.
- Published updates appear on public pages through the normal refresh path without manual backend intervention.
- Deleted and unpublished pages stop resolving publicly after revalidation.
- Reader requests for warm cached pages do not require a backend fetch.
- Failed revalidation events are visible, filterable, and retryable in the admin workflow.
