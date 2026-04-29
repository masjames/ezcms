# Webhook Spec

## Purpose

- Define strict cache revalidation behavior for content mutations and recovery behavior when revalidation fails.

## Rules

- Cache revalidation for published content changes MUST be triggered only after the content transaction commits successfully.
- The system MUST NOT invoke the revalidation webhook from inside an open database transaction.
- The following content events MUST trigger revalidation: publish article, update published article, unpublish article, delete article, update metadata for published content.
- Saving an unpublished draft MUST NOT trigger revalidation.
- Revalidation requests MUST support targeted path invalidation.
- Administrative recovery MUST support tenant-wide revalidation by tenant cache tag.
- If the first webhook attempt fails, the system MUST retry with backoff of 2 seconds, 4 seconds, and 8 seconds, for a maximum of 4 total attempts.
- After the final failed attempt, the system MUST persist the failure to a PostgreSQL dead-letter table.
- Dead-letter persistence MUST survive process restart and redeploy.
- Dead-letter records MUST include at least: `id`, `tenant_id`, `payload_json`, `status`, `attempt_count`, `last_attempted_at`, `last_error`, `created_at`.
- `page_path`, `cache_tag`, and `resolved_at` MAY be nullable but MUST be available when applicable.
- Dead-letter status progression MUST support `pending`, `failed`, `retried`, and `resolved`.
- The admin workflow MUST expose dead-letter entries with filtering and retry actions.
- Retrying a dead-letter entry MUST use the same revalidation contract as the original event.
- Webhook replay workers and admin retries MUST be idempotent.

## Failure Modes

- In-transaction webhook calls hold open database connections when the frontend times out or is unavailable.
- Missing revalidation on unpublish or delete leaves stale public pages live until cache expiry.
- Retry state held only in memory is lost on restart and creates silent stale content.
- No dead-letter visibility forces discovery through reader complaints rather than the admin tool.
- Non-idempotent retry behavior can create duplicate work or inconsistent retry status.

## Observability

- Emit structured logs for every webhook attempt with tenant, target path or cache tag, attempt number, latency, and result.
- Persist final failures in `webhook_failures` with timestamp and last error message.
- Expose dead-letter entries in the admin UI with filter by tenant, status, and date range.
- Track counts of successful revalidations, retries, and dead-letter writes.
- Alert when failed or pending dead-letter volume exceeds normal operational thresholds.
