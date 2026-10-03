package main

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/ArtemPak289/Currency-exchange-rate-service/api"
	"github.com/ArtemPak289/Currency-exchange-rate-service/internal/config"
	"github.com/ArtemPak289/Currency-exchange-rate-service/internal/exchange"
	appHTTP "github.com/ArtemPak289/Currency-exchange-rate-service/internal/handler/http"
	"github.com/ArtemPak289/Currency-exchange-rate-service/internal/repository"
	"github.com/ArtemPak289/Currency-exchange-rate-service/internal/repository/memory"
	"github.com/ArtemPak289/Currency-exchange-rate-service/internal/repository/postgres"
	"github.com/ArtemPak289/Currency-exchange-rate-service/internal/service"
	"github.com/ArtemPak289/Currency-exchange-rate-service/internal/worker"
	_ "github.com/jackc/pgx/v5/stdlib"
)

func main() {
	// 1. Load configuration
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to load config: %v\n", err)
		os.Exit(1)
	}

	// 2. Setup structured logging
	setupLogger(cfg.LogLevel, cfg.Environment)

	slog.Info("Starting Currency Exchange Rate Service",
		"env", cfg.Environment,
		"port", cfg.HTTPPort,
		"worker_count", cfg.WorkerCount,
		"mock_fetcher", cfg.UseMockFetcher,
	)

	// 3. Connect to Database (with retry logic)
	var repo repository.QuoteRepository
	var db *sql.DB

	if cfg.DatabaseURL != "" {
		var connErr error
		db, connErr = initDBWithRetry(cfg.DatabaseURL, 5, 2*time.Second)
		if connErr != nil {
			slog.Error("Database connection failed, falling back to in-memory repository", "err", connErr)
			repo = memory.NewRepository()
		} else {
			defer db.Close()

			// Run migrations
			migCtx, migCancel := context.WithTimeout(context.Background(), 15*time.Second)
			if err := postgres.RunMigrations(migCtx, db); err != nil {
				migCancel()
				slog.Error("Failed to apply database migrations", "err", err)
				os.Exit(1)
			}
			migCancel()

			repo = postgres.NewQuoteRepository(db)
		}
	} else {
		slog.Warn("No DATABASE_URL provided, running with in-memory repository")
		repo = memory.NewRepository()
	}

	// 4. Initialize Exchange Rate Fetcher
	var fetcher exchange.Fetcher
	if cfg.UseMockFetcher {
		slog.Info("Using Mock Exchange Fetcher")
		fetcher = exchange.NewMockFetcher()
	} else {
		slog.Info("Using ExchangeRatesAPI Provider", "base_url", cfg.APIBaseURL)
		fetcher = exchange.NewExchangeRatesAPIClient(
			cfg.APIKey,
			cfg.APIBaseURL,
			cfg.APITimeout,
			cfg.APICacheTTL,
		)
	}

	// 5. Initialize Background Worker Pool
	workerPool := worker.NewPool(
		repo,
		fetcher,
		cfg.WorkerCount,
		cfg.WorkerQueueSize,
		cfg.WorkerMaxRetries,
		cfg.WorkerRetryDelay,
	)

	workerCtx, workerCancel := context.WithCancel(context.Background())
	defer workerCancel()
	workerPool.Start(workerCtx)

	// 6. Initialize Business Service
	quoteService := service.NewQuoteService(repo, workerPool, cfg.DeduplicationWindow)

	// 7. Setup HTTP Handlers and Router
	quoteHandler := appHTTP.NewQuoteHandler(quoteService)

	dbPingFunc := func(ctx context.Context) error {
		if db == nil {
			return nil
		}
		return db.PingContext(ctx)
	}

	router := appHTTP.NewRouter(quoteHandler, dbPingFunc, api.OpenAPISpec, api.SwaggerJSON, api.SwaggerUIHTML)

	server := &http.Server{
		Addr:         cfg.ServerAddr(),
		Handler:      router,
		ReadTimeout:  cfg.ReadTimeout,
		WriteTimeout: cfg.WriteTimeout,
		IdleTimeout:  cfg.IdleTimeout,
	}

	// 8. Run HTTP server in separate goroutine
	serverErr := make(chan error, 1)
	go func() {
		slog.Info("HTTP server listening",
			"addr", server.Addr,
			"swagger_docs", fmt.Sprintf("http://localhost:%s/swagger/", cfg.HTTPPort),
		)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
	}()

	// 9. Graceful shutdown on SIGINT / SIGTERM
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	select {
	case err := <-serverErr:
		slog.Error("HTTP server failed to start", "err", err)
	case sig := <-quit:
		slog.Info("Received termination signal, shutting down gracefully...", "signal", sig.String())
	}

	// 10. Shutdown sequence
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer shutdownCancel()

	// Stop accepting new HTTP requests
	if err := server.Shutdown(shutdownCtx); err != nil {
		slog.Error("Error during server shutdown", "err", err)
	}

	// Stop worker pool and wait for active jobs
	workerPool.Stop()

	slog.Info("Service stopped gracefully")
}

func setupLogger(levelStr, env string) {
	var level slog.Level
	switch strings.ToLower(levelStr) {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}

	var handler slog.Handler
	opts := &slog.HandlerOptions{Level: level}

	if env == "production" {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	} else {
		handler = slog.NewTextHandler(os.Stdout, opts)
	}

	slog.SetDefault(slog.New(handler))
}

func initDBWithRetry(databaseURL string, maxRetries int, delay time.Duration) (*sql.DB, error) {
	var db *sql.DB
	var err error

	for i := 1; i <= maxRetries; i++ {
		slog.Info("Attempting to connect to PostgreSQL...", "attempt", i, "max_retries", maxRetries)

		db, err = sql.Open("pgx", databaseURL)
		if err == nil {
			db.SetMaxOpenConns(25)
			db.SetMaxIdleConns(10)
			db.SetConnMaxLifetime(5 * time.Minute)

			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			err = db.PingContext(ctx)
			cancel()

			if err == nil {
				slog.Info("Successfully connected to PostgreSQL")
				return db, nil
			}
			_ = db.Close()
		}

		slog.Warn("PostgreSQL not ready, retrying in...", "delay", delay, "err", err)
		time.Sleep(delay)
	}

	return nil, fmt.Errorf("failed to connect to PostgreSQL after %d attempts: %w", maxRetries, err)
}
