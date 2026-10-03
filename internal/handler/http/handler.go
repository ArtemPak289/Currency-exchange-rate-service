package http

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/go-chi/cors"
)

// DBChecker represents a function that tests database availability for readiness probe.
type DBChecker func(ctx context.Context) error

// NewRouter constructs and configures the HTTP router with middleware and endpoints.
func NewRouter(quoteHandler *QuoteHandler, dbChecker DBChecker, openAPISpec []byte, swaggerJSON []byte, swaggerIndexHTML []byte) http.Handler {
	r := chi.NewRouter()

	// Standard middleware
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(LoggingMiddleware)
	r.Use(middleware.Recoverer)

	// Permissive CORS for easy API testing and browser clients
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"*"},
		AllowedMethods:   []string{"GET", "POST", "PUT", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type", "Idempotency-Key"},
		ExposedHeaders:   []string{"Link", "Idempotent-Replayed"},
		AllowCredentials: true,
		MaxAge:           300,
	}))

	// Health and Readiness probes (standard for Kubernetes / Docker)
	r.Get("/health", func(w http.ResponseWriter, r *http.Request) {
		JSON(w, http.StatusOK, map[string]string{
			"status": "healthy",
		})
	})

	r.Get("/ready", func(w http.ResponseWriter, r *http.Request) {
		if dbChecker != nil {
			if err := dbChecker(r.Context()); err != nil {
				Error(w, http.StatusServiceUnavailable, "database connection error: "+err.Error())
				return
			}
		}
		JSON(w, http.StatusOK, map[string]string{
			"status": "ready",
		})
	})

	// Swagger / OpenAPI documentation endpoints
	if len(openAPISpec) > 0 {
		r.Get("/swagger/openapi.yaml", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/yaml; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(openAPISpec)
		})
		r.Get("/swagger", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/swagger/", http.StatusMovedPermanently)
		})
		r.Get("/docs", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/swagger/", http.StatusMovedPermanently)
		})
	}
	if len(swaggerJSON) > 0 {
		r.Get("/swagger/swagger.json", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(swaggerJSON)
		})
	}
	if len(swaggerIndexHTML) > 0 {
		r.Get("/swagger/", func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(swaggerIndexHTML)
		})
	}

	// API v1 routes
	r.Route("/api/v1", func(r chi.Router) {
		r.Route("/quotes", func(r chi.Router) {
			// 1. Update quote (POST)
			r.Post("/", quoteHandler.RefreshQuote)
			r.Post("/refresh", quoteHandler.RefreshQuote)

			// 3. Get latest quote (GET /api/v1/quotes/latest?currency=EUR/MXN)
			r.Get("/latest", quoteHandler.GetLatestQuote)
			r.Get("/latest/{currency}", quoteHandler.GetLatestQuote)

			// 2. Get quote by ID (GET /api/v1/quotes/{id})
			r.Get("/{id}", quoteHandler.GetQuoteByID)
		})
	})

	return r
}
