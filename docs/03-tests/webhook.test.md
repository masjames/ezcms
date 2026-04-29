# Webhook Integrity Tests

## Scenario

- Revalidation must occur only after a successful commit, and webhook failure must not hold database resources open.

## Setup

- Seed a published article path.
- Configure the frontend revalidation endpoint to time out or fail deterministically.
- Instrument database connection usage and transaction duration.

## Execution

- Trigger a publish or update flow for the article.
- Force the first webhook attempt to fail.
- Observe transaction completion and subsequent retry behavior.

## Assertions

- The content mutation commits before the first webhook attempt begins.
- Database connections are released immediately after commit, not after webhook completion.
- The failed webhook is retried with backoff intervals of 2 seconds, 4 seconds, and 8 seconds.
- After maximum attempts, a dead-letter record is written with tenant, target, attempt count, and last error.

## Scenario

- Delete and unpublish must trigger the same recovery path as publish, including durable dead-letter tracking on failure.

## Setup

- Seed one published article and warm its public page in cache.
- Configure the frontend revalidation endpoint to fail for the affected path.

## Execution

- Execute unpublish for the article.
- Repeat with delete for a separate published article.
- Allow all retry attempts to exhaust.

## Assertions

- Both unpublish and delete trigger webhook attempts.
- Both failures produce dead-letter records tied to the correct tenant and path.
- Dead-letter records are visible for later admin retry rather than existing only in logs.

## Scenario

- Admin retry must resolve a dead-letter item without duplicating inconsistent state.

## Setup

- Seed a failed dead-letter record for a known path.
- Restore the frontend revalidation endpoint to success.

## Execution

- Invoke the admin retry action for the failed item.
- Invoke the same retry again to confirm idempotent behavior.

## Assertions

- The first retry succeeds and marks the record as resolved or equivalent terminal success state.
- The second retry does not create duplicate unresolved failure records for the same successful replay.
- The target page is revalidated through the same contract used by the original webhook flow.
