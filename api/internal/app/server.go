package app

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"log"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	"ezcms/api/internal/config"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/feature/s3/manager"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/chai2010/webp"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/image/draw"
)

type Server struct {
	cfg        config.Config
	db         *pgxpool.Pool
	router     *http.ServeMux
	httpClient *http.Client
	storage    *Storage
	retryBackoffs []time.Duration
}

type Storage struct {
	bucket     string
	cdnBaseURL string
	uploader   *manager.Uploader
}

type tenantContextKey struct{}

type article struct {
	ID          string     `json:"id"`
	TenantID    string     `json:"tenant_id"`
	Title       string     `json:"title"`
	Slug        string     `json:"slug"`
	Body        string     `json:"body"`
	Status      string     `json:"status"`
	PublishedAt *time.Time `json:"published_at,omitempty"`
	UpdatedAt   time.Time  `json:"updated_at"`
	CreatedAt   time.Time  `json:"created_at"`
}

type articleInput struct {
	Title string `json:"title"`
	Slug  string `json:"slug"`
	Body  string `json:"body"`
}

type revalidatePayload struct {
	Path     string `json:"path,omitempty"`
	TenantID string `json:"tenant_id,omitempty"`
}

type webhookFailure struct {
	ID              string     `json:"id"`
	TenantID        string     `json:"tenant_id"`
	PagePath        *string    `json:"page_path,omitempty"`
	CacheTag        *string    `json:"cache_tag,omitempty"`
	Status          string     `json:"status"`
	AttemptCount    int        `json:"attempt_count"`
	LastError       *string    `json:"last_error,omitempty"`
	LastAttemptedAt *time.Time `json:"last_attempted_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
	ResolvedAt      *time.Time `json:"resolved_at,omitempty"`
}

func NewServer(cfg config.Config) (*Server, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	db, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("connect postgres: %w", err)
	}

	if err := db.Ping(ctx); err != nil {
		return nil, fmt.Errorf("ping postgres: %w", err)
	}

	storage, err := newStorage(ctx, cfg)
	if err != nil {
		return nil, err
	}

	server := &Server{
		cfg: cfg,
		db:  db,
		httpClient: &http.Client{
			Timeout: time.Duration(cfg.WebhookTimeoutMS) * time.Millisecond,
		},
		storage: storage,
		retryBackoffs: buildWebhookBackoffs(cfg.WebhookMaxAttempts),
	}

	server.router = server.routes()
	return server, nil
}

func (s *Server) Router() http.Handler {
	return s.router
}

func (s *Server) Close() {
	s.db.Close()
}

func newStorage(ctx context.Context, cfg config.Config) (*Storage, error) {
	resolver := aws.EndpointResolverWithOptionsFunc(func(service, region string, _ ...interface{}) (aws.Endpoint, error) {
		if service == s3.ServiceID {
			return aws.Endpoint{
				URL:               cfg.S3Endpoint,
				HostnameImmutable: true,
			}, nil
		}
		return aws.Endpoint{}, fmt.Errorf("unsupported service %s", service)
	})

	awsCfg, err := awsconfig.LoadDefaultConfig(
		ctx,
		awsconfig.WithRegion(cfg.S3Region),
		awsconfig.WithEndpointResolverWithOptions(resolver),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(
			cfg.S3AccessKeyID,
			cfg.S3SecretAccessKey,
			"",
		)),
	)
	if err != nil {
		return nil, fmt.Errorf("load aws config: %w", err)
	}

	client := s3.NewFromConfig(awsCfg, func(opts *s3.Options) {
		opts.UsePathStyle = cfg.S3ForcePathStyle
	})

	return &Storage{
		bucket:     cfg.S3Bucket,
		cdnBaseURL: strings.TrimRight(cfg.CDNBaseURL, "/"),
		uploader:   manager.NewUploader(client),
	}, nil
}

func (s *Server) routes() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.HandleFunc("/admin/articles", s.withAdminAuth(s.handleAdminArticles))
	mux.HandleFunc("/admin/articles/", s.withAdminAuth(s.handleAdminArticleByID))
	mux.HandleFunc("/admin/revalidate", s.withAdminAuth(s.handleAdminRevalidate))
	mux.HandleFunc("/admin/webhook-failures", s.withAdminAuth(s.handleWebhookFailures))
	mux.HandleFunc("/media", s.withAdminAuth(s.handleMediaUpload))
	mux.HandleFunc("/public/", s.handlePublicRoutes)
	return mux
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	if err := s.db.Ping(ctx); err != nil {
		writeError(w, http.StatusServiceUnavailable, "database unavailable")
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) withAdminAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		authHeader := strings.TrimSpace(r.Header.Get("Authorization"))
		if !strings.HasPrefix(authHeader, "Bearer ") {
			writeError(w, http.StatusUnauthorized, "missing bearer token")
			return
		}

		tokenString := strings.TrimPrefix(authHeader, "Bearer ")
		token, err := jwt.Parse(tokenString, func(token *jwt.Token) (interface{}, error) {
			if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("unexpected signing method")
			}
			return []byte(s.cfg.JWTSecret), nil
		})
		if err != nil || !token.Valid {
			writeError(w, http.StatusUnauthorized, "invalid token")
			return
		}

		claims, ok := token.Claims.(jwt.MapClaims)
		if !ok {
			writeError(w, http.StatusUnauthorized, "invalid claims")
			return
		}

		tenantID, _ := claims["tenant_id"].(string)
		if tenantID == "" {
			writeError(w, http.StatusUnauthorized, "missing tenant_id")
			return
		}

		ctx := context.WithValue(r.Context(), tenantContextKey{}, tenantID)
		next(w, r.WithContext(ctx))
	}
}

func (s *Server) handleAdminArticles(w http.ResponseWriter, r *http.Request) {
	tenantID := mustTenantID(r.Context())
	switch r.Method {
	case http.MethodGet:
		articles, err := s.listArticles(r.Context(), tenantID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, articles)
	case http.MethodPost:
		var input articleInput
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			writeError(w, http.StatusBadRequest, "invalid json")
			return
		}
		created, err := s.createDraft(r.Context(), tenantID, input)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusCreated, created)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) handleAdminArticleByID(w http.ResponseWriter, r *http.Request) {
	tenantID := mustTenantID(r.Context())
	path := strings.TrimPrefix(r.URL.Path, "/admin/articles/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, http.StatusNotFound, "article not found")
		return
	}

	id := parts[0]
	action := ""
	if len(parts) > 1 {
		action = parts[1]
	}

	switch {
	case r.Method == http.MethodPut && action == "":
		var input articleInput
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			writeError(w, http.StatusBadRequest, "invalid json")
			return
		}
		updated, err := s.updateArticle(r.Context(), tenantID, id, input)
		if err != nil {
			handleDomainError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, updated)
	case r.Method == http.MethodPost && action == "publish":
		published, err := s.publishArticle(r.Context(), tenantID, id)
		if err != nil {
			handleDomainError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, published)
	case r.Method == http.MethodPost && action == "unpublish":
		updated, err := s.unpublishArticle(r.Context(), tenantID, id)
		if err != nil {
			handleDomainError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, updated)
	case r.Method == http.MethodDelete && action == "":
		if err := s.deleteArticle(r.Context(), tenantID, id); err != nil {
			handleDomainError(w, err)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
	}
}

func (s *Server) handleAdminRevalidate(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	tenantID := mustTenantID(r.Context())
	var payload revalidatePayload
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid json")
		return
	}

	if payload.Path == "" && payload.TenantID == "" {
		writeError(w, http.StatusBadRequest, "path or tenant_id required")
		return
	}
	if payload.TenantID == "" {
		payload.TenantID = tenantID
	}

	if err := s.revalidateWithRetry(r.Context(), payload.TenantID, payload); err != nil {
		writeError(w, http.StatusBadGateway, err.Error())
		return
	}

	writeJSON(w, http.StatusOK, map[string]string{"status": "revalidated"})
}

func (s *Server) handleWebhookFailures(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	tenantID := mustTenantID(r.Context())
	failures, err := s.listWebhookFailures(r.Context(), tenantID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, failures)
}

func (s *Server) handlePublicRoutes(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/public/"), "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] != "articles" {
		writeError(w, http.StatusNotFound, "route not found")
		return
	}

	tenantID := parts[0]
	switch len(parts) {
	case 2:
		articles, err := s.listPublishedArticles(r.Context(), tenantID)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, articles)
	case 3:
		slug := parts[2]
		if slug == "" {
			writeError(w, http.StatusNotFound, "route not found")
			return
		}
		article, err := s.getPublishedArticle(r.Context(), tenantID, slug)
		if err != nil {
			handleDomainError(w, err)
			return
		}
		writeJSON(w, http.StatusOK, article)
	default:
		writeError(w, http.StatusNotFound, "route not found")
	}
}

func (s *Server) handleMediaUpload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}

	tenantID := mustTenantID(r.Context())
	if err := r.ParseMultipartForm(20 << 20); err != nil {
		writeError(w, http.StatusBadRequest, "invalid multipart form")
		return
	}

	file, _, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "file is required")
		return
	}
	defer file.Close()

	record, err := s.processMediaUpload(r.Context(), tenantID, file)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	writeJSON(w, http.StatusCreated, record)
}

func (s *Server) listArticles(ctx context.Context, tenantID string) ([]article, error) {
	var articles []article
	err := s.withTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, tenant_id, title, slug, body, status, published_at, updated_at, created_at
			FROM articles
			ORDER BY created_at DESC
		`)
		if err != nil {
			return err
		}
		defer rows.Close()

		for rows.Next() {
			var a article
			if err := rows.Scan(&a.ID, &a.TenantID, &a.Title, &a.Slug, &a.Body, &a.Status, &a.PublishedAt, &a.UpdatedAt, &a.CreatedAt); err != nil {
				return err
			}
			articles = append(articles, a)
		}
		return rows.Err()
	})
	return articles, err
}

func (s *Server) createDraft(ctx context.Context, tenantID string, input articleInput) (article, error) {
	if err := validateArticleInput(input); err != nil {
		return article{}, err
	}

	var created article
	err := s.withTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			INSERT INTO articles (tenant_id, title, slug, body, status)
			VALUES ($1, $2, $3, $4, 'draft')
			RETURNING id, tenant_id, title, slug, body, status, published_at, updated_at, created_at
		`, tenantID, input.Title, input.Slug, input.Body).
			Scan(&created.ID, &created.TenantID, &created.Title, &created.Slug, &created.Body, &created.Status, &created.PublishedAt, &created.UpdatedAt, &created.CreatedAt)
	})
	return created, mapPgError(err)
}

func (s *Server) updateArticle(ctx context.Context, tenantID, articleID string, input articleInput) (article, error) {
	if err := validateArticleInput(input); err != nil {
		return article{}, err
	}

	var updated article
	var shouldRevalidate bool
	var path string

	err := s.withTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		var existingStatus string
		if err := tx.QueryRow(ctx, `SELECT status FROM articles WHERE id = $1`, articleID).Scan(&existingStatus); err != nil {
			return err
		}

		if err := tx.QueryRow(ctx, `
			UPDATE articles
			SET title = $2, slug = $3, body = $4, updated_at = NOW()
			WHERE id = $1
			RETURNING id, tenant_id, title, slug, body, status, published_at, updated_at, created_at
		`, articleID, input.Title, input.Slug, input.Body).
			Scan(&updated.ID, &updated.TenantID, &updated.Title, &updated.Slug, &updated.Body, &updated.Status, &updated.PublishedAt, &updated.UpdatedAt, &updated.CreatedAt); err != nil {
			return err
		}

		if existingStatus == "published" {
			shouldRevalidate = true
			path = articlePath(tenantID, updated.Slug)
		}
		return nil
	})
	if err != nil {
		return article{}, mapPgError(err)
	}

	if shouldRevalidate {
		if err := s.revalidateWithRetry(ctx, tenantID, revalidatePayload{Path: path}); err != nil {
			log.Printf("revalidate update: %v", err)
		}
	}

	return updated, nil
}

func (s *Server) publishArticle(ctx context.Context, tenantID, articleID string) (article, error) {
	var published article
	var path string
	err := s.withTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `
			UPDATE articles
			SET status = 'published', published_at = COALESCE(published_at, NOW()), updated_at = NOW()
			WHERE id = $1
			RETURNING id, tenant_id, title, slug, body, status, published_at, updated_at, created_at
		`, articleID).Scan(&published.ID, &published.TenantID, &published.Title, &published.Slug, &published.Body, &published.Status, &published.PublishedAt, &published.UpdatedAt, &published.CreatedAt); err != nil {
			return err
		}
		path = articlePath(tenantID, published.Slug)
		return nil
	})
	if err != nil {
		return article{}, mapPgError(err)
	}

	if err := s.revalidateWithRetry(ctx, tenantID, revalidatePayload{Path: path}); err != nil {
		log.Printf("revalidate publish: %v", err)
	}
	return published, nil
}

func (s *Server) unpublishArticle(ctx context.Context, tenantID, articleID string) (article, error) {
	var updated article
	var path string
	err := s.withTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `
			UPDATE articles
			SET status = 'draft', updated_at = NOW()
			WHERE id = $1
			RETURNING id, tenant_id, title, slug, body, status, published_at, updated_at, created_at
		`, articleID).Scan(&updated.ID, &updated.TenantID, &updated.Title, &updated.Slug, &updated.Body, &updated.Status, &updated.PublishedAt, &updated.UpdatedAt, &updated.CreatedAt); err != nil {
			return err
		}
		path = articlePath(tenantID, updated.Slug)
		return nil
	})
	if err != nil {
		return article{}, mapPgError(err)
	}

	if err := s.revalidateWithRetry(ctx, tenantID, revalidatePayload{Path: path}); err != nil {
		log.Printf("revalidate unpublish: %v", err)
	}
	return updated, nil
}

func (s *Server) deleteArticle(ctx context.Context, tenantID, articleID string) error {
	var path string
	err := s.withTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		if err := tx.QueryRow(ctx, `SELECT slug FROM articles WHERE id = $1`, articleID).Scan(&path); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM articles WHERE id = $1`, articleID); err != nil {
			return err
		}
		path = articlePath(tenantID, path)
		return nil
	})
	if err != nil {
		return mapPgError(err)
	}

	if err := s.revalidateWithRetry(ctx, tenantID, revalidatePayload{Path: path}); err != nil {
		log.Printf("revalidate delete: %v", err)
	}
	return nil
}

func (s *Server) getPublishedArticle(ctx context.Context, tenantID, slug string) (article, error) {
	var result article
	err := s.withTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		return tx.QueryRow(ctx, `
			SELECT id, tenant_id, title, slug, body, status, published_at, updated_at, created_at
			FROM articles
			WHERE slug = $1 AND status = 'published'
		`, slug).Scan(&result.ID, &result.TenantID, &result.Title, &result.Slug, &result.Body, &result.Status, &result.PublishedAt, &result.UpdatedAt, &result.CreatedAt)
	})
	if err != nil {
		return article{}, mapPgError(err)
	}
	return result, nil
}

func (s *Server) listPublishedArticles(ctx context.Context, tenantID string) ([]article, error) {
	var results []article
	err := s.withTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, tenant_id, title, slug, body, status, published_at, updated_at, created_at
			FROM articles
			WHERE status = 'published'
			ORDER BY published_at DESC NULLS LAST, created_at DESC
		`)
		if err != nil {
			return err
		}
		defer rows.Close()

		for rows.Next() {
			var item article
			if err := rows.Scan(&item.ID, &item.TenantID, &item.Title, &item.Slug, &item.Body, &item.Status, &item.PublishedAt, &item.UpdatedAt, &item.CreatedAt); err != nil {
				return err
			}
			results = append(results, item)
		}
		return rows.Err()
	})
	return results, err
}

func (s *Server) withTenantTx(ctx context.Context, tenantID string, fn func(tx pgx.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return err
	}

	defer func() {
		if err := tx.Rollback(ctx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
			log.Printf("rollback tenant tx: %v", err)
		}
	}()

	if _, err := tx.Exec(ctx, `SELECT set_config('app.current_tenant_id', $1, true)`, tenantID); err != nil {
		return err
	}

	if err := fn(tx); err != nil {
		return err
	}

	return tx.Commit(ctx)
}

func (s *Server) revalidateWithRetry(ctx context.Context, tenantID string, payload revalidatePayload) error {
	backoffs := s.retryBackoffs
	if len(backoffs) == 0 {
		backoffs = []time.Duration{0}
	}
	var lastErr error

	for attempt, wait := range backoffs {
		if wait > 0 {
			select {
			case <-time.After(wait):
			case <-ctx.Done():
				return ctx.Err()
			}
		}

		lastErr = s.postRevalidate(ctx, payload)
		if lastErr == nil {
			return s.resolveFailure(ctx, tenantID, payload)
		}
		if err := s.recordWebhookAttempt(ctx, tenantID, payload, attempt+1, "retried", lastErr); err != nil {
			log.Printf("record retry attempt: %v", err)
		}
	}

	if err := s.recordWebhookAttempt(ctx, tenantID, payload, len(backoffs), "failed", lastErr); err != nil {
		log.Printf("record failed webhook: %v", err)
	}
	return fmt.Errorf("revalidate failed after %d attempts: %w", len(backoffs), lastErr)
}

func buildWebhookBackoffs(maxAttempts int) []time.Duration {
	if maxAttempts <= 1 {
		return []time.Duration{0}
	}

	backoffs := make([]time.Duration, 0, maxAttempts)
	backoffs = append(backoffs, 0)
	wait := 2 * time.Second
	for len(backoffs) < maxAttempts {
		backoffs = append(backoffs, wait)
		wait *= 2
	}
	return backoffs
}

func (s *Server) postRevalidate(ctx context.Context, payload revalidatePayload) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.cfg.FrontendRevalidateURL, strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+s.cfg.FrontendRevalidateToken)
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return fmt.Errorf("revalidate status %d: %s", resp.StatusCode, strings.TrimSpace(string(message)))
	}

	return nil
}

func (s *Server) recordWebhookAttempt(ctx context.Context, tenantID string, payload revalidatePayload, attempt int, status string, cause error) error {
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	return s.withTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		var existingID string
		err := tx.QueryRow(ctx, `
			SELECT id
			FROM webhook_failures
			WHERE tenant_id = $1
			  AND COALESCE(page_path, '') = COALESCE($2, '')
			  AND COALESCE(cache_tag, '') = COALESCE($3, '')
			  AND status <> 'resolved'
			ORDER BY created_at DESC
			LIMIT 1
		`, tenantID, nullableString(payload.Path), nullableString(tenantCacheTag(payload.TenantID))).Scan(&existingID)
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return err
		}

		now := time.Now().UTC()
		if existingID == "" {
			_, err = tx.Exec(ctx, `
				INSERT INTO webhook_failures (
					tenant_id, page_path, cache_tag, payload_json, status,
					attempt_count, last_attempted_at, last_error
				)
				VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
			`, tenantID, nullableString(payload.Path), nullableString(tenantCacheTag(payload.TenantID)), payloadJSON, status, attempt, now, cause.Error())
			return err
		}

		_, err = tx.Exec(ctx, `
			UPDATE webhook_failures
			SET payload_json = $2,
			    status = $3,
			    attempt_count = $4,
			    last_attempted_at = $5,
			    last_error = $6
			WHERE id = $1
		`, existingID, payloadJSON, status, attempt, now, cause.Error())
		return err
	})
}

func (s *Server) resolveFailure(ctx context.Context, tenantID string, payload revalidatePayload) error {
	return s.withTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			UPDATE webhook_failures
			SET status = 'resolved',
			    resolved_at = NOW(),
			    last_error = NULL
			WHERE tenant_id = $1
			  AND COALESCE(page_path, '') = COALESCE($2, '')
			  AND COALESCE(cache_tag, '') = COALESCE($3, '')
			  AND status <> 'resolved'
		`, tenantID, nullableString(payload.Path), nullableString(tenantCacheTag(payload.TenantID)))
		return err
	})
}

func (s *Server) listWebhookFailures(ctx context.Context, tenantID string) ([]webhookFailure, error) {
	var failures []webhookFailure
	err := s.withTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		rows, err := tx.Query(ctx, `
			SELECT id, tenant_id, page_path, cache_tag, status, attempt_count,
			       last_error, last_attempted_at, created_at, resolved_at
			FROM webhook_failures
			ORDER BY created_at DESC
		`)
		if err != nil {
			return err
		}
		defer rows.Close()

		for rows.Next() {
			var item webhookFailure
			if err := rows.Scan(&item.ID, &item.TenantID, &item.PagePath, &item.CacheTag, &item.Status, &item.AttemptCount, &item.LastError, &item.LastAttemptedAt, &item.CreatedAt, &item.ResolvedAt); err != nil {
				return err
			}
			failures = append(failures, item)
		}
		return rows.Err()
	})
	return failures, err
}

func (s *Server) processMediaUpload(ctx context.Context, tenantID string, file multipart.File) (map[string]string, error) {
	src, _, err := image.Decode(file)
	if err != nil {
		return nil, fmt.Errorf("decode image: %w", err)
	}

	mediaID := uuid.NewString()
	variants := map[string]int{
		"hero":  1600,
		"og":    1200,
		"thumb": 400,
	}

	urls := make(map[string]string, len(variants))
	for variant, width := range variants {
		data, err := encodeVariant(src, width)
		if err != nil {
			return nil, err
		}

		key := fmt.Sprintf("media/%s/%s.webp", mediaID, variant)
		if err := s.storage.putObject(ctx, key, data); err != nil {
			return nil, err
		}
		urls[variant] = fmt.Sprintf("%s/%s", s.storage.cdnBaseURL, key)
	}

	if err := s.withTenantTx(ctx, tenantID, func(tx pgx.Tx) error {
		_, err := tx.Exec(ctx, `
			INSERT INTO media (id, tenant_id, hero_url, og_url, thumb_url)
			VALUES ($1, $2, $3, $4, $5)
		`, mediaID, tenantID, urls["hero"], urls["og"], urls["thumb"])
		return err
	}); err != nil {
		return nil, err
	}

	return map[string]string{
		"id":        mediaID,
		"hero_url":  urls["hero"],
		"og_url":    urls["og"],
		"thumb_url": urls["thumb"],
	}, nil
}

func encodeVariant(src image.Image, maxWidth int) ([]byte, error) {
	bounds := src.Bounds()
	width := bounds.Dx()
	height := bounds.Dy()
	targetWidth := min(width, maxWidth)
	targetHeight := (height * targetWidth) / width
	if targetHeight <= 0 {
		targetHeight = 1
	}

	dst := image.NewRGBA(image.Rect(0, 0, targetWidth, targetHeight))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, bounds, draw.Over, nil)

	var output bytes.Buffer
	if err := webp.Encode(&output, dst, &webp.Options{Quality: 75, Lossless: false}); err != nil {
		return nil, fmt.Errorf("encode webp: %w", err)
	}
	return output.Bytes(), nil
}

func (s *Storage) putObject(ctx context.Context, key string, body []byte) error {
	_, err := s.uploader.Upload(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(s.bucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader(body),
		ContentType: aws.String("image/webp"),
		ACL:         types.ObjectCannedACLPublicRead,
	})
	if err != nil {
		return fmt.Errorf("upload object %s: %w", key, err)
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(payload); err != nil {
		log.Printf("write json: %v", err)
	}
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func validateArticleInput(input articleInput) error {
	switch {
	case strings.TrimSpace(input.Title) == "":
		return fmt.Errorf("title is required")
	case strings.TrimSpace(input.Slug) == "":
		return fmt.Errorf("slug is required")
	default:
		return nil
	}
}

func mustTenantID(ctx context.Context) string {
	tenantID, _ := ctx.Value(tenantContextKey{}).(string)
	return tenantID
}

func articlePath(tenantID, slug string) string {
	return fmt.Sprintf("/%s/posts/%s", tenantID, slug)
}

func tenantCacheTag(tenantID string) string {
	if tenantID == "" {
		return ""
	}
	return "tenant:" + tenantID
}

func nullableString(value string) interface{} {
	if value == "" {
		return nil
	}
	return value
}

func mapPgError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("not found")
	}
	return err
}

func handleDomainError(w http.ResponseWriter, err error) {
	if err == nil {
		return
	}
	status := http.StatusInternalServerError
	message := err.Error()
	switch message {
	case "not found":
		status = http.StatusNotFound
	case "title is required", "slug is required":
		status = http.StatusBadRequest
	}
	writeError(w, status, message)
}
