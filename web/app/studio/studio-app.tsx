"use client";

import { FormEvent, useEffect, useState, useTransition } from "react";
import type { Article, WebhookFailure } from "@/lib/ezcms";

type DraftForm = {
  title: string;
  slug: string;
  body: string;
};

const apiBaseUrl =
  process.env.NEXT_PUBLIC_PUBLIC_API_BASE_URL ??
  process.env.PUBLIC_API_BASE_URL ??
  "http://localhost:8080";

export function StudioApp() {
  const [tenant, setTenant] = useState("acme");
  const [token, setToken] = useState("");
  const [form, setForm] = useState<DraftForm>({ title: "", slug: "", body: "" });
  const [articles, setArticles] = useState<Article[]>([]);
  const [failures, setFailures] = useState<WebhookFailure[]>([]);
  const [error, setError] = useState("");
  const [status, setStatus] = useState("Idle");
  const [isPending, startTransition] = useTransition();

  useEffect(() => {
    const savedTenant = window.localStorage.getItem("ezcms.tenant");
    const savedToken = window.localStorage.getItem("ezcms.token");
    if (savedTenant) setTenant(savedTenant);
    if (savedToken) setToken(savedToken);
  }, []);

  useEffect(() => {
    window.localStorage.setItem("ezcms.tenant", tenant);
  }, [tenant]);

  useEffect(() => {
    window.localStorage.setItem("ezcms.token", token);
  }, [token]);

  async function refreshAll() {
    if (!token.trim()) {
      setError("Token required");
      return;
    }

    setError("");
    setStatus("Refreshing");

    const [articlesResponse, failuresResponse] = await Promise.all([
      fetch(`${apiBaseUrl}/admin/articles`, { headers: authHeaders(token) }),
      fetch(`${apiBaseUrl}/admin/webhook-failures`, { headers: authHeaders(token) }),
    ]);

    if (!articlesResponse.ok) {
      setError(await extractError(articlesResponse));
      setStatus("Failed");
      return;
    }
    if (!failuresResponse.ok) {
      setError(await extractError(failuresResponse));
      setStatus("Failed");
      return;
    }

    setArticles(await articlesResponse.json());
    setFailures(await failuresResponse.json());
    setStatus("Ready");
  }

  async function handleCreate(event: FormEvent) {
    event.preventDefault();
    startTransition(async () => {
      setError("");
      const response = await fetch(`${apiBaseUrl}/admin/articles`, {
        method: "POST",
        headers: authHeaders(token),
        body: JSON.stringify(form),
      });

      if (!response.ok) {
        setError(await extractError(response));
        return;
      }

      setForm({ title: "", slug: "", body: "" });
      await refreshAll();
    });
  }

  async function runArticleAction(articleId: string, method: string, action = "") {
    startTransition(async () => {
      setError("");
      const response = await fetch(`${apiBaseUrl}/admin/articles/${articleId}${action ? `/${action}` : ""}`, {
        method,
        headers: authHeaders(token),
      });

      if (!response.ok) {
        setError(await extractError(response));
        return;
      }
      await refreshAll();
    });
  }

  async function retryFailure(path?: string | null, targetTenant?: string | null) {
    startTransition(async () => {
      setError("");
      const payload = path ? { path } : { tenant_id: targetTenant || tenant };
      const response = await fetch(`${apiBaseUrl}/admin/revalidate`, {
        method: "POST",
        headers: authHeaders(token),
        body: JSON.stringify(payload),
      });

      if (!response.ok) {
        setError(await extractError(response));
        return;
      }
      await refreshAll();
    });
  }

  return (
    <>
      <section className="studio-grid">
        <article className="card studio-panel">
          <span className="label">Session</span>
          <label className="field">
            <span>Tenant</span>
            <input value={tenant} onChange={(event) => setTenant(event.target.value)} />
          </label>
          <label className="field">
            <span>JWT</span>
            <textarea
              rows={5}
              value={token}
              onChange={(event) => setToken(event.target.value)}
              placeholder="Paste the token from scripts/dev-jwt.mjs"
            />
          </label>
          <div className="button-row">
            <button onClick={() => startTransition(refreshAll)} disabled={isPending}>
              Refresh data
            </button>
            <a href={`/${tenant}`} target="_blank" rel="noreferrer">
              Open tenant site
            </a>
          </div>
          <p className="lede">{status}</p>
          {error ? <p className="error-text">{error}</p> : null}
        </article>

        <article className="card studio-panel">
          <span className="label">Create Draft</span>
          <form className="field-stack" onSubmit={handleCreate}>
            <label className="field">
              <span>Title</span>
              <input value={form.title} onChange={(event) => setForm((current) => ({ ...current, title: event.target.value }))} />
            </label>
            <label className="field">
              <span>Slug</span>
              <input value={form.slug} onChange={(event) => setForm((current) => ({ ...current, slug: event.target.value }))} />
            </label>
            <label className="field">
              <span>Body</span>
              <textarea rows={7} value={form.body} onChange={(event) => setForm((current) => ({ ...current, body: event.target.value }))} />
            </label>
            <button type="submit" disabled={isPending}>Create draft</button>
          </form>
        </article>
      </section>

      <section className="grid">
        <article className="card studio-panel">
          <span className="label">Articles</span>
          {articles.length === 0 ? (
            <p className="lede">No articles loaded yet.</p>
          ) : (
            <div className="list-stack">
              {articles.map((article) => (
                <div className="row-card" key={article.id}>
                  <div>
                    <strong>{article.title}</strong>
                    <div className="meta">{article.slug} · {article.status}</div>
                  </div>
                  <div className="button-row">
                    {article.status === "draft" ? (
                      <button onClick={() => runArticleAction(article.id, "POST", "publish")} disabled={isPending}>Publish</button>
                    ) : (
                      <button onClick={() => runArticleAction(article.id, "POST", "unpublish")} disabled={isPending}>Unpublish</button>
                    )}
                    <button onClick={() => runArticleAction(article.id, "DELETE")} disabled={isPending}>Delete</button>
                    <a href={`/${tenant}/posts/${article.slug}`} target="_blank" rel="noreferrer">View</a>
                  </div>
                </div>
              ))}
            </div>
          )}
        </article>

        <article className="card studio-panel">
          <span className="label">Webhook Failures</span>
          {failures.length === 0 ? (
            <p className="lede">No webhook failures recorded.</p>
          ) : (
            <div className="list-stack">
              <div className="button-row">
                <button onClick={() => retryFailure(undefined, tenant)} disabled={isPending}>Retry tenant cache</button>
              </div>
              {failures.map((failure) => (
                <div className="row-card" key={failure.id}>
                  <div>
                    <strong>{failure.page_path || failure.cache_tag || "tenant-wide"}</strong>
                    <div className="meta">{failure.status} · attempts {failure.attempt_count}</div>
                    {failure.last_error ? <div className="meta">{failure.last_error}</div> : null}
                  </div>
                  <div className="button-row">
                    <button onClick={() => retryFailure(failure.page_path, failure.tenant_id)} disabled={isPending}>Retry</button>
                  </div>
                </div>
              ))}
            </div>
          )}
        </article>
      </section>
    </>
  );
}

function authHeaders(token: string) {
  return {
    Authorization: `Bearer ${token}`,
    "Content-Type": "application/json",
  };
}

async function extractError(response: Response) {
  try {
    const payload = await response.json();
    if (typeof payload.error === "string") {
      return payload.error;
    }
  } catch {
    return `${response.status} ${response.statusText}`;
  }
  return `${response.status} ${response.statusText}`;
}
