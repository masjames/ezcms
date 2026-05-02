export default function HomePage() {
  return (
    <main className="shell">
      <section className="frame">
        <header className="banner">
          <span className="label">EzCMS Local Stack</span>
          <h1>Parity-first build for tenancy, ISR, and media delivery.</h1>
          <p>
            Start by creating a draft through the API, publish it with a tenant JWT,
            then open a route such as <code>/acme/posts/hello-world</code>.
          </p>
        </header>

        <section className="grid">
          <article className="card">
            <span className="label">Backend</span>
            <p className="lede">
              Go API on <code>localhost:8080</code> with request-scoped tenancy and
              post-commit revalidation.
            </p>
          </article>
          <article className="card">
            <span className="label">Frontend</span>
            <p className="lede">
              Next.js ISR on <code>localhost:3000</code> with explicit revalidate
              semantics and <code>notFound()</code> for removed content.
            </p>
          </article>
          <article className="card">
            <span className="label">Object Storage</span>
            <p className="lede">
              MinIO stands in for R2 locally while keeping the code on an S3-compatible
              path.
            </p>
          </article>
        </section>
      </section>
    </main>
  );
}

