package test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/ArtemPak289/Currency-exchange-rate-service/api"
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

func TestEndToEndLifecycle(t *testing.T) {
	ctx := context.Background()

	// Setup in-memory test environment
	repo := memory.NewRepository()
	mockFetcher := exchange.NewMockFetcher()
	mockFetcher.SetRate("EUR/MXN", 20.447892)
	mockFetcher.SetRate("USD/MXN", 18.160810)

	pool := worker.NewPool(repo, mockFetcher, 2, 50, 2, 5*time.Millisecond)
	pool.Start(ctx)
	defer pool.Stop()

	svc := service.NewQuoteService(repo, pool, 15*time.Second)
	quoteHandler := appHTTP.NewQuoteHandler(svc)
	router := appHTTP.NewRouter(quoteHandler, func(ctx context.Context) error { return nil }, api.OpenAPISpec, api.SwaggerJSON, api.SwaggerUIHTML)

	server := httptest.NewServer(router)
	defer server.Close()

	client := server.Client()

	// Step 1: Health & Ready & Swagger endpoints
	t.Run("Health and Swagger endpoints work", func(t *testing.T) {
		resp, err := client.Get(server.URL + "/health")
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		_ = resp.Body.Close()

		resp, err = client.Get(server.URL + "/ready")
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		_ = resp.Body.Close()

		resp, err = client.Get(server.URL + "/swagger/")
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		_ = resp.Body.Close()

		resp, err = client.Get(server.URL + "/swagger/openapi.yaml")
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		_ = resp.Body.Close()

		resp, err = client.Get(server.URL + "/swagger/swagger.json")
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		_ = resp.Body.Close()
	})

	// Step 2: Query latest quote before any update -> 404
	t.Run("Querying latest quote before refresh returns 404", func(t *testing.T) {
		resp, err := client.Get(server.URL + "/api/v1/quotes/latest?currency=EUR/MXN")
		require.NoError(t, err)
		assert.Equal(t, http.StatusNotFound, resp.StatusCode)
		_ = resp.Body.Close()
	})

	// Step 3: Trigger quote refresh -> 202 Accepted
	var requestID uuid.UUID
	idempKey := "e2e-client-key-100"

	t.Run("POST /api/v1/quotes schedules refresh asynchronously", func(t *testing.T) {
		payload := []byte(`{"currency": "EUR/MXN"}`)
		httpReq, err := http.NewRequest(http.MethodPost, server.URL+"/api/v1/quotes", bytes.NewBuffer(payload))
		require.NoError(t, err)
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("Idempotency-Key", idempKey)

		resp, err := client.Do(httpReq)
		require.NoError(t, err)
		assert.Equal(t, http.StatusAccepted, resp.StatusCode)

		var updateResp appHTTP.UpdateQuoteResponse
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&updateResp))
		_ = resp.Body.Close()

		assert.NotEqual(t, uuid.Nil, updateResp.ID)
		assert.Equal(t, "EUR/MXN", updateResp.Currency)
		assert.Equal(t, domain.StatusPending, updateResp.Status)

		requestID = updateResp.ID
	})

	// Step 4: Duplicate request with same Idempotency-Key returns same ID
	t.Run("Duplicate request with same Idempotency-Key returns existing ID", func(t *testing.T) {
		payload := []byte(`{"currency": "EUR/MXN"}`)
		httpReq, err := http.NewRequest(http.MethodPost, server.URL+"/api/v1/quotes", bytes.NewBuffer(payload))
		require.NoError(t, err)
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("Idempotency-Key", idempKey)

		resp, err := client.Do(httpReq)
		require.NoError(t, err)
		assert.True(t, resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusAccepted)
		assert.Equal(t, "true", resp.Header.Get("Idempotent-Replayed"))

		var updateResp appHTTP.UpdateQuoteResponse
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&updateResp))
		_ = resp.Body.Close()

		assert.Equal(t, requestID, updateResp.ID)
	})

	// Step 5: Wait for background worker to complete the job, then verify GET /api/v1/quotes/{id}
	t.Run("Poll GET /api/v1/quotes/{id} until COMPLETED", func(t *testing.T) {
		var detail appHTTP.QuoteDetailResponse

		require.Eventually(t, func() bool {
			resp, err := client.Get(server.URL + "/api/v1/quotes/" + requestID.String())
			if err != nil || resp.StatusCode != http.StatusOK {
				return false
			}
			defer resp.Body.Close()

			if err := json.NewDecoder(resp.Body).Decode(&detail); err != nil {
				return false
			}

			return detail.Status == domain.StatusCompleted
		}, 3*time.Second, 20*time.Millisecond)

		assert.Equal(t, requestID, detail.ID)
		assert.Equal(t, "EUR/MXN", detail.Currency)
		assert.Equal(t, domain.StatusCompleted, detail.Status)
		require.NotNil(t, detail.Price)
		assert.Equal(t, 20.447892, *detail.Price)
		assert.False(t, detail.UpdatedAt.IsZero())
	})

	// Step 6: Verify GET /api/v1/quotes/latest?currency=EUR/MXN returns the updated price
	t.Run("GET /api/v1/quotes/latest returns latest updated rate", func(t *testing.T) {
		resp, err := client.Get(server.URL + "/api/v1/quotes/latest?currency=EUR/MXN")
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var latest appHTTP.LatestQuoteResponse
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&latest))
		_ = resp.Body.Close()

		assert.Equal(t, "EUR/MXN", latest.Currency)
		assert.Equal(t, 20.447892, latest.Price)
		assert.False(t, latest.UpdatedAt.IsZero())
	})

	// Step 7: Verify Uzbek Som / SUMM colloquial alias support
	t.Run("Support Uzbek Som / SUMM", func(t *testing.T) {
		payload := []byte(`{"currency": "USD/SUMM"}`)
		httpReq, err := http.NewRequest(http.MethodPost, server.URL+"/api/v1/quotes", bytes.NewBuffer(payload))
		require.NoError(t, err)
		httpReq.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(httpReq)
		require.NoError(t, err)
		assert.Equal(t, http.StatusAccepted, resp.StatusCode)

		var updateResp appHTTP.UpdateQuoteResponse
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&updateResp))
		_ = resp.Body.Close()

		assert.Equal(t, "USD/UZS", updateResp.Currency)

		// Wait for completion
		require.Eventually(t, func() bool {
			r, err := client.Get(server.URL + "/api/v1/quotes/" + updateResp.ID.String())
			if err != nil || r.StatusCode != http.StatusOK {
				return false
			}
			defer r.Body.Close()
			var detail appHTTP.QuoteDetailResponse
			if err := json.NewDecoder(r.Body).Decode(&detail); err != nil {
				return false
			}
			return detail.Status == domain.StatusCompleted
		}, 3*time.Second, 20*time.Millisecond)

		// Query latest by canonical USD/UZS
		resp, err = client.Get(server.URL + "/api/v1/quotes/latest?currency=USD/UZS")
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)

		var latest appHTTP.LatestQuoteResponse
		require.NoError(t, json.NewDecoder(resp.Body).Decode(&latest))
		_ = resp.Body.Close()

		assert.Equal(t, "USD/UZS", latest.Currency)
		assert.Equal(t, 11768.733734, latest.Price)

		// Also query latest by alias USD/SUMM
		resp, err = client.Get(server.URL + "/api/v1/quotes/latest?currency=USD/SUMM")
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, resp.StatusCode)
		_ = resp.Body.Close()
	})
}
