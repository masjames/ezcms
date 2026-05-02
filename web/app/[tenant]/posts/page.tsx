import Link from "next/link";
import { fetchPublishedArticles } from "@/lib/ezcms";

type Params = Promise<{ tenant: string }>;

export const revalidate = 60;

export default async function TenantPostsPage({ params }: { params: Params }) {
  const { tenant } = await params;
  const articles = await fetchPublishedArticles(tenant);

  return (
    <main className="shell">
      <section className="frame">
        <header className="banner">
          <span className="label">{tenant}</span>
          <h1>Published posts</h1>
          <p>Tenant-visible content list backed by the public read contract.</p>
        </header>

        <section className="grid">
          {articles.length === 0 ? (
            <article className="card">
              <p className="lede">No published posts yet.</p>
            </article>
          ) : (
            articles.map((article) => (
              <article className="card" key={article.id}>
                <span className="label">Live</span>
                <h2>{article.title}</h2>
                <p className="lede">{excerpt(article.body)}</p>
                <Link href={`/${tenant}/posts/${article.slug}`}>Open article</Link>
              </article>
            ))
          )}
        </section>
      </section>
    </main>
  );
}

function excerpt(body: string) {
  return body.length > 180 ? `${body.slice(0, 177)}...` : body;
}
