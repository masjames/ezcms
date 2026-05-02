import { notFound } from "next/navigation";
import { fetchPublishedArticle } from "@/lib/ezcms";

export const revalidate = 60;

type Params = Promise<{ tenant: string; slug: string }>;

export default async function ArticlePage({ params }: { params: Params }) {
  const { tenant, slug } = await params;
  const article = await fetchPublishedArticle(tenant, slug);
  if (!article) {
    notFound();
  }

  return (
    <main className="shell">
      <article className="frame article">
        <span className="label">{tenant}</span>
        <h1>{article.title}</h1>
        <div className="meta">
          Updated {new Date(article.updated_at).toLocaleString()}
        </div>
        <div className="article-body">{article.body}</div>
      </article>
    </main>
  );
}
