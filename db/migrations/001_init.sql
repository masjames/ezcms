CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TYPE article_status AS ENUM ('draft', 'published');

CREATE TABLE IF NOT EXISTS articles (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id TEXT NOT NULL,
    title TEXT NOT NULL,
    slug TEXT NOT NULL,
    body TEXT NOT NULL DEFAULT '',
    status article_status NOT NULL DEFAULT 'draft',
    published_at TIMESTAMPTZ NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (tenant_id, slug)
);

CREATE TABLE IF NOT EXISTS media (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id TEXT NOT NULL,
    hero_url TEXT NOT NULL,
    og_url TEXT NOT NULL,
    thumb_url TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS webhook_failures (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    tenant_id TEXT NOT NULL,
    page_path TEXT NULL,
    cache_tag TEXT NULL,
    payload_json JSONB NOT NULL,
    status TEXT NOT NULL CHECK (status IN ('pending', 'failed', 'retried', 'resolved')),
    attempt_count INTEGER NOT NULL DEFAULT 0,
    last_attempted_at TIMESTAMPTZ NULL,
    last_error TEXT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    resolved_at TIMESTAMPTZ NULL
);

CREATE INDEX IF NOT EXISTS webhook_failures_tenant_status_created_idx
    ON webhook_failures (tenant_id, status, created_at DESC);

CREATE INDEX IF NOT EXISTS webhook_failures_status_attempted_idx
    ON webhook_failures (status, last_attempted_at);

ALTER TABLE articles ENABLE ROW LEVEL SECURITY;
ALTER TABLE media ENABLE ROW LEVEL SECURITY;
ALTER TABLE webhook_failures ENABLE ROW LEVEL SECURITY;
ALTER TABLE articles FORCE ROW LEVEL SECURITY;
ALTER TABLE media FORCE ROW LEVEL SECURITY;
ALTER TABLE webhook_failures FORCE ROW LEVEL SECURITY;

DROP POLICY IF EXISTS tenant_isolation_articles ON articles;
CREATE POLICY tenant_isolation_articles ON articles
    USING (tenant_id = current_setting('app.current_tenant_id', true))
    WITH CHECK (tenant_id = current_setting('app.current_tenant_id', true));

DROP POLICY IF EXISTS tenant_isolation_media ON media;
CREATE POLICY tenant_isolation_media ON media
    USING (tenant_id = current_setting('app.current_tenant_id', true))
    WITH CHECK (tenant_id = current_setting('app.current_tenant_id', true));

DROP POLICY IF EXISTS tenant_isolation_webhook_failures ON webhook_failures;
CREATE POLICY tenant_isolation_webhook_failures ON webhook_failures
    USING (tenant_id = current_setting('app.current_tenant_id', true))
    WITH CHECK (tenant_id = current_setting('app.current_tenant_id', true));

GRANT ALL PRIVILEGES ON TABLE articles TO ezcms;
GRANT ALL PRIVILEGES ON TABLE media TO ezcms;
GRANT ALL PRIVILEGES ON TABLE webhook_failures TO ezcms;
