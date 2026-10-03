package config

import (
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/joho/godotenv"
)

// Config holds all configuration options for the service.
type Config struct {
	// Server
	HTTPPort     string
	ReadTimeout  time.Duration
	WriteTimeout time.Duration
	IdleTimeout  time.Duration

	// Database
	DatabaseURL string

	// External Exchange API
	APIKey         string
	APIBaseURL     string
	APITimeout     time.Duration
	APICacheTTL    time.Duration
	UseMockFetcher bool

	// Background Worker
	WorkerCount         int
	WorkerQueueSize     int
	WorkerMaxRetries    int
	WorkerRetryDelay    time.Duration
	DeduplicationWindow time.Duration

	// Application
	Environment string
	LogLevel    string
}

// Load loads configuration from environment variables and optionally a .env file.
func Load() (*Config, error) {
	// Best-effort load from .env file (ignore error if file does not exist)
	_ = godotenv.Load()

	cfg := &Config{
		HTTPPort:            getEnv("HTTP_PORT", "8080"),
		ReadTimeout:         getEnvDuration("HTTP_READ_TIMEOUT", 10*time.Second),
		WriteTimeout:        getEnvDuration("HTTP_WRITE_TIMEOUT", 15*time.Second),
		IdleTimeout:         getEnvDuration("HTTP_IDLE_TIMEOUT", 60*time.Second),
		DatabaseURL:         getEnv("DATABASE_URL", "postgres://postgres:postgres@localhost:5432/quotes?sslmode=disable"),
		APIKey:              getEnv("EXCHANGE_API_KEY", "2270434cad5887ab4ccc50fd5d8cfd16"),
		APIBaseURL:          getEnv("EXCHANGE_API_BASE_URL", "http://api.exchangeratesapi.io/v1/latest"),
		APITimeout:          getEnvDuration("EXCHANGE_API_TIMEOUT", 10*time.Second),
		APICacheTTL:         getEnvDuration("EXCHANGE_API_CACHE_TTL", 30*time.Second),
		UseMockFetcher:      getEnvBool("USE_MOCK_FETCHER", false),
		WorkerCount:         getEnvInt("WORKER_COUNT", 3),
		WorkerQueueSize:     getEnvInt("WORKER_QUEUE_SIZE", 1000),
		WorkerMaxRetries:    getEnvInt("WORKER_MAX_RETRIES", 3),
		WorkerRetryDelay:    getEnvDuration("WORKER_RETRY_DELAY", 1*time.Second),
		DeduplicationWindow: getEnvDuration("DEDUPLICATION_WINDOW", 15*time.Second),
		Environment:         getEnv("ENVIRONMENT", "development"),
		LogLevel:            getEnv("LOG_LEVEL", "info"),
	}

	return cfg, nil
}

func getEnv(key, defaultVal string) string {
	if val, ok := os.LookupEnv(key); ok && val != "" {
		return val
	}
	return defaultVal
}

func getEnvInt(key string, defaultVal int) int {
	if val, ok := os.LookupEnv(key); ok && val != "" {
		if i, err := strconv.Atoi(val); err == nil {
			return i
		}
	}
	return defaultVal
}

func getEnvBool(key string, defaultVal bool) bool {
	if val, ok := os.LookupEnv(key); ok && val != "" {
		if b, err := strconv.ParseBool(val); err == nil {
			return b
		}
	}
	return defaultVal
}

func getEnvDuration(key string, defaultVal time.Duration) time.Duration {
	if val, ok := os.LookupEnv(key); ok && val != "" {
		if d, err := time.ParseDuration(val); err == nil {
			return d
		}
	}
	return defaultVal
}

// ServerAddr returns the formatted TCP address to listen on.
func (c *Config) ServerAddr() string {
	return fmt.Sprintf(":%s", c.HTTPPort)
}
