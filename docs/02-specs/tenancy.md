# Tenancy Spec

## Purpose

- Define the non-bypassable rules for tenant isolation in request handling and PostgreSQL access.

## Rules

- Every authenticated request that accesses tenant-scoped data MUST derive a single `tenant_id` from the validated JWT.
- The extracted `tenant_id` MUST be attached to request-scoped context only.
- Tenant context MUST NOT be stored in process-global state, shared mutable state, or connection-global state.
- Every repository operation against tenant-scoped tables MUST run inside a database transaction.
- At the start of each such transaction, the repository MUST execute `SET LOCAL app.current_tenant_id = <tenant_id>`.
- The system MUST rely on PostgreSQL RLS for row filtering of tenant-scoped tables.
- Application queries against tenant-scoped tables MUST NOT depend on handler-added `WHERE tenant_id = ...` clauses as the primary isolation mechanism.
- RLS policies for tenant-scoped tables MUST compare row `tenant_id` against `current_setting('app.current_tenant_id')`.
- The implementation MUST use transaction-scoped tenant settings compatible with transaction-mode pooling.
- The implementation MUST NOT use `SET SESSION` for tenant context.
- On commit or rollback, tenant context MUST be cleared automatically by transaction scope before the connection returns to the pool.
- A request for tenant B MUST NOT be able to observe tenant A's `current_setting` value on a reused pooled connection.

## Failure Modes

- `SET SESSION` or equivalent persistent connection state causes tenant context leakage across pooled requests.
- Missing `SET LOCAL` allows RLS lookups to evaluate against an empty or stale tenant context.
- Tenant context stored outside request scope causes cross-request contamination under concurrency.
- Missing or incorrect RLS policy permits application bugs to bypass tenant isolation.
- Queries executed outside the transaction that established tenant context can run without correct isolation.

## Observability

- Log request tenant identity and database transaction boundaries with a shared request identifier.
- Expose query-time verification hooks or debug instrumentation that can report the current database tenant setting during tests and incident analysis.
- Alert on any detected mismatch between request tenant identity and query-time tenant setting.
- Run concurrency tests that prove no cross-tenant rows are returned under pooled connection reuse.
