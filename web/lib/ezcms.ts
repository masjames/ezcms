export type Article = {
  id: string;
  tenant_id: string;
  title: string;
  slug: string;
  body: string;
  status: string;
  published_at?: string;
  updated_at: string;
  created_at: string;
};

export type WebhookFailure = {
  id: string;
  tenant_id: string;
  page_path?: string | null;
  cache_tag?: string | null;
  status: string;
  attempt_count: number;
  last_error?: string | null;
  last_attempted_at?: string | null;
  created_at: string;
  resolved_at?: string | null;
};

export function getApiBaseUrl() {
  const baseUrl = process.env.PUBLIC_API_BASE_URL;
  if (!baseUrl) {
    throw new Error("PUBLIC_API_BASE_URL is required");
  }
  return baseUrl;
}

export async function fetchPublishedArticles(tenant: string): Promise<Article[]> {
  const response = await fetch(`${getApiBaseUrl()}/public/${tenant}/articles`, {
    next: {
      revalidate: 60,
      tags: [`tenant:${tenant}`],
    },
  });

  if (!response.ok) {
    throw new Error(`Failed to fetch articles: ${response.status}`);
  }

  return response.json();
}

export async function fetchPublishedArticle(tenant: string, slug: string): Promise<Article | null> {
  const response = await fetch(`${getApiBaseUrl()}/public/${tenant}/articles/${slug}`, {
    next: {
      revalidate: 60,
      tags: [`tenant:${tenant}`, `article:${tenant}:${slug}`],
    },
  });

  if (response.status === 404) {
    return null;
  }
  if (!response.ok) {
    throw new Error(`Failed to fetch article: ${response.status}`);
  }

  return response.json();
}
