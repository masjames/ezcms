# Tenancy Integrity Tests

## Scenario

- Concurrent cross-tenant read load must not leak tenant context across pooled database connections.

## Setup

- Seed at least two tenants, `tenant_a` and `tenant_b`.
- Create tenant-scoped rows for both tenants with easily distinguishable identifiers.
- Run the application against transaction-mode pooled PostgreSQL access.
- Add an instrumented read path that returns:
- Rows visible to the request.
- The query-time value of `current_setting('app.current_tenant_id')`.

## Execution

- First run serial requests for `tenant_a` and `tenant_b` to establish the baseline.
- Then fire high-concurrency mixed requests for both tenants with rapid connection reuse.
- Repeat enough iterations to force pooled connections to serve both tenants over time.

## Assertions

- Every request for `tenant_a` reports query-time tenant setting `tenant_a`.
- Every request for `tenant_b` reports query-time tenant setting `tenant_b`.
- No `tenant_a` request returns any row owned by `tenant_b`.
- No `tenant_b` request returns any row owned by `tenant_a`.
- No request reports a stale tenant setting from a previous request.

## Scenario

- Transaction rollback must clear request tenant context before the connection is reused.

## Setup

- Use the same pooled environment and seeded tenant data.
- Add a request path that intentionally rolls back after setting tenant context.

## Execution

- Issue a request for `tenant_a` that begins a transaction, sets tenant context, and rolls back.
- Immediately follow with a normal read request for `tenant_b` on the same pool under repeated reuse pressure.

## Assertions

- The `tenant_b` request reports query-time tenant setting `tenant_b`, not `tenant_a`.
- The `tenant_b` request returns only `tenant_b` rows.
