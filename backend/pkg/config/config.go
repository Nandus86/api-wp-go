package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	// Core
	HTTPPort   string
	DBDialect  string
	DBAddress  string
	DataDir    string
	LogLevel   string
	WALogLevel string
	Debug      bool

	// Infra
	RabbitMQURL string
	RedisURL    string
	RedisPass   string
	RedisDB     int

	MinioEndpoint  string
	MinioAccessKey string
	MinioSecretKey string
	MinioUseSSL    bool
	MinioBucket    string
	MinioPublicURL string

	// Auth / security
	AuthRequired    bool
	AdminAPIKey     string
	CORSOrigins     []string
	AllowedCIDRs    []string
	TrustProxy      bool
	MaxBodyBytes    int64
	RateLimitPerMin int
	MetricsToken    string

	// Webhooks
	WebhookURL           string
	WebhookSecret        string
	WebhookTimeout       time.Duration
	WebhookMaxAttempts   int
	WebhookWorkers       int
	WebhookRetentionDays int

	// Messaging
	MessageWorkers        int
	MessageMaxAttempts    int
	MessageRetentionDays  int
	MediaArchiveEnabled   bool
	MediaMaxBytes         int64
	SimulateTyping        bool
	SendPacingEnabled     bool
	SendPacingSchedule    []int
	SendPacingColdCap     int
	SendPacingFailBreaker int

	ShutdownTimeout time.Duration
}

func Load() *Config {
	return &Config{
		HTTPPort:   getEnv("HTTP_PORT", "8080"),
		DBDialect:  getEnv("DB_DIALECT", "postgres"),
		DBAddress:  getEnv("DB_ADDRESS", "postgres://user:pass@localhost/dbname?sslmode=disable"),
		DataDir:    getEnv("DATA_DIR", "./data"),
		LogLevel:   strings.ToLower(getEnv("LOG_LEVEL", "info")),
		WALogLevel: strings.ToUpper(getEnv("WA_LOG_LEVEL", "INFO")),
		Debug:      getBool("DEBUG_MESSAGES", false),

		RabbitMQURL: getEnv("RABBITMQ_URL", "amqp://whatsmeow:securepassword@localhost:5675/"),
		RedisURL:    getEnv("REDIS_URL", ""),
		RedisPass:   getEnv("REDIS_PASSWORD", ""),
		RedisDB:     getInt("REDIS_DB", 0),

		MinioEndpoint:  getEnv("MINIO_ENDPOINT", ""),
		MinioAccessKey: getEnv("MINIO_ACCESS_KEY", ""),
		MinioSecretKey: getEnv("MINIO_SECRET_KEY", ""),
		MinioUseSSL:    getBool("MINIO_USE_SSL", false),
		MinioBucket:    getEnv("MINIO_BUCKET", "whatsmeow-media"),
		MinioPublicURL: strings.TrimRight(getEnv("MINIO_PUBLIC_URL", ""), "/"),

		AuthRequired:    getBool("AUTH_REQUIRED", true),
		AdminAPIKey:     getEnv("ADMIN_API_KEY", ""),
		CORSOrigins:     getList("CORS_ORIGINS", []string{"*"}),
		AllowedCIDRs:    getList("ALLOWED_CIDRS", nil),
		TrustProxy:      getBool("TRUST_PROXY", true),
		MaxBodyBytes:    int64(getInt("MAX_BODY_MB", 64)) << 20,
		RateLimitPerMin: getInt("RATE_LIMIT_PER_MINUTE", 600),
		MetricsToken:    getEnv("METRICS_TOKEN", ""),

		WebhookURL:           getEnv("WEBHOOK_URL", ""),
		WebhookSecret:        getEnv("WEBHOOK_SECRET", ""),
		WebhookTimeout:       time.Duration(getInt("WEBHOOK_TIMEOUT_SECONDS", 15)) * time.Second,
		WebhookMaxAttempts:   getInt("WEBHOOK_MAX_ATTEMPTS", 8),
		WebhookWorkers:       getInt("WEBHOOK_WORKERS", 8),
		WebhookRetentionDays: getInt("WEBHOOK_RETENTION_DAYS", 7),

		MessageWorkers:        getInt("MESSAGE_WORKERS", 4),
		MessageMaxAttempts:    getInt("MESSAGE_MAX_ATTEMPTS", 5),
		MessageRetentionDays:  getInt("MESSAGE_RETENTION_DAYS", 0),
		MediaArchiveEnabled:   getBool("MEDIA_ARCHIVE_ENABLED", false),
		MediaMaxBytes:         int64(getInt("MEDIA_MAX_MB", 64)) << 20,
		SimulateTyping:        getBool("SIMULATE_TYPING", false),
		SendPacingEnabled:     getBool("SEND_PACING_ENABLED", false),
		SendPacingSchedule:    getIntList("SEND_PACING_WARMUP_SCHEDULE", []int{50, 100, 200, 400, 800, 1500}),
		SendPacingColdCap:     getInt("SEND_PACING_COLD_DAILY_CAP", 50),
		SendPacingFailBreaker: getInt("SEND_PACING_FAILURE_BREAKER", 10),

		ShutdownTimeout: time.Duration(getInt("SHUTDOWN_TIMEOUT_SECONDS", 20)) * time.Second,
	}
}

// Validate returns every configuration problem at once so the operator can fix them in one pass.
func (c *Config) Validate() error {
	var errs []string
	if c.DBDialect != "postgres" {
		errs = append(errs, fmt.Sprintf("DB_DIALECT=%q is not supported (only postgres)", c.DBDialect))
	}
	if c.DBAddress == "" {
		errs = append(errs, "DB_ADDRESS is required")
	}
	if c.RabbitMQURL == "" {
		errs = append(errs, "RABBITMQ_URL is required")
	}
	if c.MinioEndpoint != "" && (c.MinioAccessKey == "" || c.MinioSecretKey == "") {
		errs = append(errs, "MINIO_ACCESS_KEY and MINIO_SECRET_KEY are required when MINIO_ENDPOINT is set")
	}
	if c.MediaArchiveEnabled && c.MinioEndpoint == "" {
		errs = append(errs, "MEDIA_ARCHIVE_ENABLED requires MINIO_ENDPOINT")
	}
	if c.AdminAPIKey != "" && len(c.AdminAPIKey) < 16 {
		errs = append(errs, "ADMIN_API_KEY must be at least 16 characters")
	}
	if c.WebhookMaxAttempts < 1 {
		errs = append(errs, "WEBHOOK_MAX_ATTEMPTS must be >= 1")
	}
	if c.MessageMaxAttempts < 1 {
		errs = append(errs, "MESSAGE_MAX_ATTEMPTS must be >= 1")
	}
	if c.WebhookWorkers < 1 || c.MessageWorkers < 1 {
		errs = append(errs, "WEBHOOK_WORKERS and MESSAGE_WORKERS must be >= 1")
	}
	if len(errs) > 0 {
		return fmt.Errorf("invalid configuration:\n  - %s", strings.Join(errs, "\n  - "))
	}
	return nil
}

func getEnv(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return strings.TrimSpace(value)
	}
	return fallback
}

func getBool(key string, fallback bool) bool {
	v, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(v) == "" {
		return fallback
	}
	b, err := strconv.ParseBool(strings.TrimSpace(v))
	if err != nil {
		return fallback
	}
	return b
}

func getInt(key string, fallback int) int {
	v, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(v) == "" {
		return fallback
	}
	i, err := strconv.Atoi(strings.TrimSpace(v))
	if err != nil {
		return fallback
	}
	return i
}

func getList(key string, fallback []string) []string {
	v, ok := os.LookupEnv(key)
	if !ok || strings.TrimSpace(v) == "" {
		return fallback
	}
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

func getIntList(key string, fallback []int) []int {
	raw := getList(key, nil)
	if len(raw) == 0 {
		return fallback
	}
	var out []int
	for _, p := range raw {
		i, err := strconv.Atoi(p)
		if err != nil {
			return fallback
		}
		out = append(out, i)
	}
	return out
}
