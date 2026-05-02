package config

import (
	"fmt"
	"os"
	"strconv"
)

type Config struct {
	DatabaseURL             string
	JWTSecret               string
	Port                    string
	CDNBaseURL              string
	FrontendRevalidateURL   string
	FrontendRevalidateToken string
	WebhookTimeoutMS        int
	WebhookMaxAttempts      int
	S3Endpoint              string
	S3Region                string
	S3Bucket                string
	S3AccessKeyID           string
	S3SecretAccessKey       string
	S3ForcePathStyle        bool
}

func Load() (Config, error) {
	cfg := Config{
		DatabaseURL:             os.Getenv("DATABASE_URL"),
		JWTSecret:               os.Getenv("JWT_SECRET"),
		Port:                    getenv("PORT", "8080"),
		CDNBaseURL:              os.Getenv("CDN_BASE_URL"),
		FrontendRevalidateURL:   os.Getenv("FRONTEND_REVALIDATE_URL"),
		FrontendRevalidateToken: os.Getenv("FRONTEND_REVALIDATE_TOKEN"),
		WebhookTimeoutMS:        getenvInt("WEBHOOK_TIMEOUT_MS", 3000),
		WebhookMaxAttempts:      getenvInt("WEBHOOK_MAX_ATTEMPTS", 4),
		S3Endpoint:              os.Getenv("S3_ENDPOINT"),
		S3Region:                getenv("S3_REGION", "us-east-1"),
		S3Bucket:                os.Getenv("S3_BUCKET"),
		S3AccessKeyID:           os.Getenv("S3_ACCESS_KEY_ID"),
		S3SecretAccessKey:       os.Getenv("S3_SECRET_ACCESS_KEY"),
		S3ForcePathStyle:        getenvBool("S3_FORCE_PATH_STYLE", true),
	}

	switch {
	case cfg.DatabaseURL == "":
		return Config{}, fmt.Errorf("DATABASE_URL is required")
	case cfg.JWTSecret == "":
		return Config{}, fmt.Errorf("JWT_SECRET is required")
	case cfg.CDNBaseURL == "":
		return Config{}, fmt.Errorf("CDN_BASE_URL is required")
	case cfg.FrontendRevalidateURL == "":
		return Config{}, fmt.Errorf("FRONTEND_REVALIDATE_URL is required")
	case cfg.FrontendRevalidateToken == "":
		return Config{}, fmt.Errorf("FRONTEND_REVALIDATE_TOKEN is required")
	case cfg.S3Endpoint == "":
		return Config{}, fmt.Errorf("S3_ENDPOINT is required")
	case cfg.S3Bucket == "":
		return Config{}, fmt.Errorf("S3_BUCKET is required")
	case cfg.S3AccessKeyID == "":
		return Config{}, fmt.Errorf("S3_ACCESS_KEY_ID is required")
	case cfg.S3SecretAccessKey == "":
		return Config{}, fmt.Errorf("S3_SECRET_ACCESS_KEY is required")
	default:
		return cfg, nil
	}
}

func getenv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func getenvInt(key string, fallback int) int {
	if value := os.Getenv(key); value != "" {
		if parsed, err := strconv.Atoi(value); err == nil {
			return parsed
		}
	}
	return fallback
}

func getenvBool(key string, fallback bool) bool {
	if value := os.Getenv(key); value != "" {
		if parsed, err := strconv.ParseBool(value); err == nil {
			return parsed
		}
	}
	return fallback
}

