package app

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"ezcms/api/internal/config"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func TestTenantIsolationUnderConcurrentLoad(t *testing.T) {
	t.Parallel()

	server := newTestServer(t)
	server.httpClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		return jsonResponse(http.StatusOK, `{}`)
	})}
	defer server.Close()

	tenantA := "tenant-a-" + uuid.NewString()
	tenantB := "tenant-b-" + uuid.NewString()

	articleA, err := server.createDraft(context.Background(), tenantA, articleInput{
		Title: "Alpha",
		Slug:  "alpha-" + uuid.NewString(),
		Body:  "belongs to tenant A",
	})
	if err != nil {
		t.Fatalf("create draft A: %v", err)
	}

	articleB, err := server.createDraft(context.Background(), tenantB, articleInput{
		Title: "Beta",
		Slug:  "beta-" + uuid.NewString(),
		Body:  "belongs to tenant B",
	})
	if err != nil {
		t.Fatalf("create draft B: %v", err)
	}

	var wg sync.WaitGroup
	errCh := make(chan error, 64)

	for i := 0; i < 32; i++ {
		wg.Add(2)

		go func() {
			defer wg.Done()
			items, err := server.listArticles(context.Background(), tenantA)
			if err != nil {
				errCh <- err
				return
			}
			if len(items) == 0 || items[0].TenantID != tenantA {
				errCh <- errTenantLeak
				return
			}
			for _, item := range items {
				if item.TenantID != tenantA || item.ID == articleB.ID {
					errCh <- errTenantLeak
					return
				}
			}
		}()

		go func() {
			defer wg.Done()
			items, err := server.listArticles(context.Background(), tenantB)
			if err != nil {
				errCh <- err
				return
			}
			if len(items) == 0 || items[0].TenantID != tenantB {
				errCh <- errTenantLeak
				return
			}
			for _, item := range items {
				if item.TenantID != tenantB || item.ID == articleA.ID {
					errCh <- errTenantLeak
					return
				}
			}
		}()
	}

	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("concurrent tenant isolation: %v", err)
	}

	var current string
	if err := server.withTenantTx(context.Background(), tenantA, func(tx pgx.Tx) error {
		return tx.QueryRow(context.Background(), `SELECT current_setting('app.current_tenant_id', true)`).Scan(&current)
	}); err != nil {
		t.Fatalf("query current_setting: %v", err)
	}
	if current != tenantA {
		t.Fatalf("expected transaction tenant %q, got %q", tenantA, current)
	}
}

func TestWebhookFailureCreatesDeadLetter(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int32
	server := newTestServer(t)
	server.httpClient = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		attempts.Add(1)
		return jsonResponse(http.StatusInternalServerError, `{"error":"forced failure"}`)
	})}
	server.retryBackoffs = []time.Duration{0, time.Millisecond, time.Millisecond}
	defer server.Close()

	tenant := "dead-letter-" + uuid.NewString()
	created, err := server.createDraft(context.Background(), tenant, articleInput{
		Title: "Broken publish",
		Slug:  "broken-" + uuid.NewString(),
		Body:  "publish should fail revalidation",
	})
	if err != nil {
		t.Fatalf("create draft: %v", err)
	}

	published, err := server.publishArticle(context.Background(), tenant, created.ID)
	if err != nil {
		t.Fatalf("publish article: %v", err)
	}
	if published.Status != "published" {
		t.Fatalf("expected published status, got %s", published.Status)
	}

	failures, err := server.listWebhookFailures(context.Background(), tenant)
	if err != nil {
		t.Fatalf("list webhook failures: %v", err)
	}
	if len(failures) != 1 {
		t.Fatalf("expected 1 failure, got %d", len(failures))
	}
	if failures[0].Status != "failed" {
		t.Fatalf("expected failure status failed, got %s", failures[0].Status)
	}
	if failures[0].AttemptCount != 3 {
		t.Fatalf("expected 3 attempts, got %d", failures[0].AttemptCount)
	}
	if failures[0].PagePath == nil || *failures[0].PagePath != articlePath(tenant, created.Slug) {
		t.Fatalf("unexpected failure path: %#v", failures[0].PagePath)
	}
	if attempts.Load() != 3 {
		t.Fatalf("expected 3 webhook attempts, got %d", attempts.Load())
	}
}

func TestPublishUnpublishDeleteTriggerRevalidation(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	var paths []string
	server := newTestServer(t)
	server.httpClient = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		defer r.Body.Close()
		var payload revalidatePayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			return nil, err
		}
		mu.Lock()
		paths = append(paths, payload.Path)
		mu.Unlock()
		return jsonResponse(http.StatusOK, `{}`)
	})}
	server.retryBackoffs = []time.Duration{0}
	defer server.Close()

	tenant := "revalidate-" + uuid.NewString()
	first, err := server.createDraft(context.Background(), tenant, articleInput{
		Title: "One",
		Slug:  "one-" + uuid.NewString(),
		Body:  "first",
	})
	if err != nil {
		t.Fatalf("create draft one: %v", err)
	}
	second, err := server.createDraft(context.Background(), tenant, articleInput{
		Title: "Two",
		Slug:  "two-" + uuid.NewString(),
		Body:  "second",
	})
	if err != nil {
		t.Fatalf("create draft two: %v", err)
	}

	if _, err := server.publishArticle(context.Background(), tenant, first.ID); err != nil {
		t.Fatalf("publish first: %v", err)
	}
	if _, err := server.unpublishArticle(context.Background(), tenant, first.ID); err != nil {
		t.Fatalf("unpublish first: %v", err)
	}
	if _, err := server.publishArticle(context.Background(), tenant, second.ID); err != nil {
		t.Fatalf("publish second: %v", err)
	}
	if err := server.deleteArticle(context.Background(), tenant, second.ID); err != nil {
		t.Fatalf("delete second: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	expected := []string{
		articlePath(tenant, first.Slug),
		articlePath(tenant, first.Slug),
		articlePath(tenant, second.Slug),
		articlePath(tenant, second.Slug),
	}
	if len(paths) != len(expected) {
		t.Fatalf("expected %d revalidation calls, got %d (%v)", len(expected), len(paths), paths)
	}
	for _, path := range expected {
		if !slices.Contains(paths, path) {
			t.Fatalf("missing revalidation path %s in %v", path, paths)
		}
	}
}

var errTenantLeak = &testError{"cross-tenant row observed"}

type testError struct {
	message string
}

func (e *testError) Error() string {
	return e.message
}

func newTestServer(t *testing.T) *Server {
	t.Helper()

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		t.Skip("DATABASE_URL is required for integration tests")
	}

	cfg := config.Config{
		DatabaseURL:             dbURL,
		JWTSecret:               "local-dev-secret",
		Port:                    "8080",
		CDNBaseURL:              "http://localhost:9000/ezcms-local",
		FrontendRevalidateURL:   "http://revalidate.local/api/revalidate",
		FrontendRevalidateToken: "local-revalidate-token",
		WebhookTimeoutMS:        200,
		WebhookMaxAttempts:      3,
		S3Endpoint:              "http://localhost:9000",
		S3Region:                "us-east-1",
		S3Bucket:                "ezcms-local",
		S3AccessKeyID:           "minioadmin",
		S3SecretAccessKey:       "minioadmin",
		S3ForcePathStyle:        true,
	}

	server, err := NewServer(cfg)
	if err != nil {
		if strings.Contains(err.Error(), "connect postgres") || strings.Contains(err.Error(), "ping postgres") {
			t.Skipf("postgres not ready for integration tests: %v", err)
		}
		t.Fatalf("new test server: %v", err)
	}
	return server
}

type roundTripFunc func(req *http.Request) (*http.Response, error)

func (fn roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return fn(req)
}

func jsonResponse(status int, body string) (*http.Response, error) {
	return &http.Response{
		StatusCode: status,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(body)),
	}, nil
}
