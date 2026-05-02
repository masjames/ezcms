import Link from "next/link";
import { fetchPublishedArticles } from "@/lib/ezcms";

type Params = Promise<{ tenant: string }>;

export const revalidate = 60;

export default async function TenantHomePage({ params }: { params: Params }) {
  const { tenant } = await params;
  const articles = await fetchPublishedArticles(tenant);
  const featured = articles[0];

  return (
    <main className="shell">
      <section className="frame">
        <header className="banner">
          <span className="label">{tenant}</span>
          <h1>Framemalang local tenant front door.</h1>
          <p>
            This tenant home uses the same ISR and public-read contract as the article
            pages. Publish in the studio, then refresh this route to verify the cache path.
          </p>
        </header>

        <section className="grid">
          <article className="card">
            <span className="label">Published Count</span>
            <p className="lede">{articles.length} live article{articles.length === 1 ? "" : "s"}.</p>
          </article>
          <article className="card">
            <span className="label">Browse</span>
            <p className="lede">
              <Link href={`/${tenant}/posts`}>Open the tenant post index</Link>
            </p>
          </article>
          <article className="card">
            <span className="label">Studio</span>
            <p className="lede">
              <Link href="/studio">Open the local studio</Link>
            </p>
          </article>
        </section>

        <section className="grid">
          <article className="card">
            <span className="label">Featured</span>
            {featured ? (
              <>
                <h2>{featured.title}</h2>
                <p className="lede">{excerpt(featured.body)}</p>
                <Link href={`/${tenant}/posts/${featured.slug}`}>Read article</Link>
              </>
            ) : (
              <p className="lede">No published articles yet for this tenant.</p>
            )}
          </article>
        </section>
      </section>
    </main>
  );
}

function excerpt(body: string) {
  return body.length > 180 ? `${body.slice(0, 177)}...` : body;
}
