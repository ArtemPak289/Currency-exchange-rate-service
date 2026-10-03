package http_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ArtemPak289/Currency-exchange-rate-service/internal/domain"
	"github.com/ArtemPak289/Currency-exchange-rate-service/internal/exchange"
	appHTTP "github.com/ArtemPak289/Currency-exchange-rate-service/internal/handler/http"
	"github.com/ArtemPak289/Currency-exchange-rate-service/internal/repository/memory"
	"github.com/ArtemPak289/Currency-exchange-rate-service/internal/service"
	"github.com/ArtemPak289/Currency-exchange-rate-service/internal/worker"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestRouter(t *testing.T) (http.Handler, *memory.Repository, *worker.Pool) {
	ctx := context.Background()
	repo := memory.NewRepository()
	mockFetcher := exchange.NewMockFetcher()
	pool := worker.NewPool(repo, mockFetcher, 2, 10, 2, 10*time.Millisecond)
	pool.Start(ctx)

	svc := service.NewQuoteService(repo, pool, 30*time.Second)
	handler := appHTTP.NewQuoteHandler(svc)
	router := appHTTP.NewRouter(handler, func(ctx context.Context) error { return nil }, nil, nil, nil)

	return router, repo, pool
}

func TestHTTPHealthAndReady(t *testing.T) {
	router, _, pool := setupTestRouter(t)
	defer pool.Stop()

	t.Run("GET /health", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/health", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Contains(t, rec.Body.String(), "healthy")
	})

	t.Run("GET /ready", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/ready", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Contains(t, rec.Body.String(), "ready")
	})
}

func TestHTTPRefreshQuote(t *testing.T) {
	router, _, pool := setupTestRouter(t)
	defer pool.Stop()

	t.Run("successful quote update scheduling returns 202 Accepted", func(t *testing.T) {
		payload := `{"currency": "EUR/MXN"}`
		req := httptest.NewRequest(http.MethodPost, "/api/v1/quotes", bytes.NewBufferString(payload))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()

		router.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusAccepted, rec.Code)

		var resp appHTTP.UpdateQuoteResponse
		err := json.Unmarshal(rec.Body.Bytes(), &resp)
		require.NoError(t, err)
		assert.NotEqual(t, uuid.Nil, resp.ID)
		assert.Equal(t, "EUR/MXN", resp.Currency)
		assert.Equal(t, domain.StatusPending, resp.Status)
	})

	t.Run("invalid currency returns 400 Bad Request", func(t *testing.T) {
		payload := `{"currency": "INVALID"}`
		req := httptest.NewRequest(http.MethodPost, "/api/v1/quotes", bytes.NewBufferString(payload))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()

		router.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Contains(t, rec.Body.String(), "invalid currency pair format")
	})

	t.Run("identical currencies returns 400 Bad Request", func(t *testing.T) {
		payload := `{"currency": "EUR/EUR"}`
		req := httptest.NewRequest(http.MethodPost, "/api/v1/quotes", bytes.NewBufferString(payload))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()

		router.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Contains(t, rec.Body.String(), "cannot be identical")
	})

	t.Run("idempotent replay returns same ID and Idempotent-Replayed header", func(t *testing.T) {
		payload := `{"currency": "USD/MXN"}`
		key := "test-idemp-1"

		// First call
		req1 := httptest.NewRequest(http.MethodPost, "/api/v1/quotes", bytes.NewBufferString(payload))
		req1.Header.Set("Content-Type", "application/json")
		req1.Header.Set("Idempotency-Key", key)
		rec1 := httptest.NewRecorder()
		router.ServeHTTP(rec1, req1)
		require.Equal(t, http.StatusAccepted, rec1.Code)

		var resp1 appHTTP.UpdateQuoteResponse
		require.NoError(t, json.Unmarshal(rec1.Body.Bytes(), &resp1))

		// Second call with same key
		req2 := httptest.NewRequest(http.MethodPost, "/api/v1/quotes", bytes.NewBufferString(payload))
		req2.Header.Set("Content-Type", "application/json")
		req2.Header.Set("Idempotency-Key", key)
		rec2 := httptest.NewRecorder()
		router.ServeHTTP(rec2, req2)

		assert.Equal(t, "true", rec2.Header().Get("Idempotent-Replayed"))

		var resp2 appHTTP.UpdateQuoteResponse
		require.NoError(t, json.Unmarshal(rec2.Body.Bytes(), &resp2))
		assert.Equal(t, resp1.ID, resp2.ID)
	})

	t.Run("idempotency conflict with different currency returns 409 Conflict", func(t *testing.T) {
		key := "conflict-key"

		// First call
		req1 := httptest.NewRequest(http.MethodPost, "/api/v1/quotes", bytes.NewBufferString(`{"currency": "EUR/USD"}`))
		req1.Header.Set("Content-Type", "application/json")
		req1.Header.Set("Idempotency-Key", key)
		rec1 := httptest.NewRecorder()
		router.ServeHTTP(rec1, req1)
		require.Equal(t, http.StatusAccepted, rec1.Code)

		// Second call with same key but different currency
		req2 := httptest.NewRequest(http.MethodPost, "/api/v1/quotes", bytes.NewBufferString(`{"currency": "EUR/MXN"}`))
		req2.Header.Set("Content-Type", "application/json")
		req2.Header.Set("Idempotency-Key", key)
		rec2 := httptest.NewRecorder()
		router.ServeHTTP(rec2, req2)

		assert.Equal(t, http.StatusConflict, rec2.Code)
		assert.Contains(t, rec2.Body.String(), "idempotency key was previously used")
	})
}

func TestHTTPGetQuoteByID(t *testing.T) {
	router, repo, pool := setupTestRouter(t)
	defer pool.Stop()

	t.Run("existing completed quote", func(t *testing.T) {
		id := uuid.New()
		price := 20.447892
		require.NoError(t, repo.CreateRequest(context.Background(), &domain.QuoteRequest{
			ID:       id,
			Currency: "EUR/MXN",
			Status:   domain.StatusCompleted,
			Price:    &price,
		}))

		req := httptest.NewRequest(http.MethodGet, "/api/v1/quotes/"+id.String(), nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)

		var resp appHTTP.QuoteDetailResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
		assert.Equal(t, id, resp.ID)
		assert.Equal(t, domain.StatusCompleted, resp.Status)
		require.NotNil(t, resp.Price)
		assert.Equal(t, price, *resp.Price)
	})

	t.Run("non-existent quote returns 404", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/quotes/"+uuid.New().String(), nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusNotFound, rec.Code)
	})

	t.Run("invalid UUID returns 400", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/quotes/not-a-uuid", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusBadRequest, rec.Code)
		assert.Contains(t, rec.Body.String(), "invalid UUID")
	})
}

func TestHTTPGetLatestQuote(t *testing.T) {
	router, repo, pool := setupTestRouter(t)
	defer pool.Stop()

	t.Run("existing latest quote returns 200 OK", func(t *testing.T) {
		require.NoError(t, repo.SaveLatestQuote(context.Background(), &domain.LatestQuote{
			Currency:  "EUR/MXN",
			Price:     20.447892,
			UpdatedAt: time.Now().UTC(),
		}))

		req := httptest.NewRequest(http.MethodGet, "/api/v1/quotes/latest?currency=EUR/MXN", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusOK, rec.Code)

		var resp appHTTP.LatestQuoteResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
		assert.Equal(t, "EUR/MXN", resp.Currency)
		assert.Equal(t, 20.447892, resp.Price)
	})

	t.Run("missing query parameter returns 400 Bad Request", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/quotes/latest", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("quote not found returns 404 Not Found", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/quotes/latest?currency=USD/CAD", nil)
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)

		assert.Equal(t, http.StatusNotFound, rec.Code)
	})
}
