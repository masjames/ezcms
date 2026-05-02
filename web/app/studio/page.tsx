import { StudioApp } from "./studio-app";

export default function StudioPage() {
  return (
    <main className="shell">
      <section className="frame">
        <header className="banner">
          <span className="label">Local Studio</span>
          <h1>Operate the CMS without curl.</h1>
          <p>
            Enter a tenant and JWT, then create drafts, publish content, and retry
            failed revalidation events from one local interface.
          </p>
        </header>
        <StudioApp />
      </section>
    </main>
  );
}
