# ISR Integrity Tests

## Scenario

- Warm cached pages must avoid backend fetches, and cold or expired rebuilds must hit upstream once per path.

## Setup

- Seed a published article path.
- Enable request counting on the backend content fetch used by the Next.js page.
- Warm the page once to establish a cached entry.

## Execution

- Issue repeated reader requests against the warm path.
- Then invalidate or expire the path and issue concurrent reader requests during rebuild.

## Assertions

- Warm-path requests do not increment backend fetch count.
- The cold or expired rebuild increments backend fetch count once for that path.
- Concurrent readers receive served content without N identical upstream fetches for the same rebuild event.

## Scenario

- Unpublish must remove a previously cached page through revalidation plus `notFound()` handling.

## Setup

- Seed a published article and warm `/posts/<slug>` in cache.
- Confirm the page currently returns `200`.

## Execution

- Unpublish the article.
- Trigger the normal revalidation path.
- Request the same public path after the rebuild cycle.

## Assertions

- The revalidation flow is invoked for the unpublished path.
- The rebuilt page resolves to `notFound()` semantics.
- The public path no longer returns the previously cached article content.
- The public path no longer returns `200` after successful revalidation.

## Scenario

- Delete must evict stale cached content rather than waiting for TTL expiry.

## Setup

- Seed a published article and warm its public path in cache.
- Confirm the page is currently served from cache.

## Execution

- Delete the article.
- Trigger the normal revalidation path.
- Request the same path after revalidation completes.

## Assertions

- The deleted path is revalidated.
- The rebuild resolves as missing content rather than stale cached content.
- The route is no longer publicly available after successful revalidation.
