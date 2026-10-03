package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/ArtemPak289/Currency-exchange-rate-service/internal/domain"
	"github.com/ArtemPak289/Currency-exchange-rate-service/internal/exchange"
	"github.com/ArtemPak289/Currency-exchange-rate-service/internal/repository/memory"
	"github.com/ArtemPak289/Currency-exchange-rate-service/internal/service"
	"github.com/ArtemPak289/Currency-exchange-rate-service/internal/worker"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestQuoteService(t *testing.T) {
	ctx := context.Background()

	setupService := func() (*service.QuoteService, *memory.Repository, *worker.Pool) {
		repo := memory.NewRepository()
		mockFetcher := exchange.NewMockFetcher()
		pool := worker.NewPool(repo, mockFetcher, 2, 10, 2, 10*time.Millisecond)
		pool.Start(ctx)
		svc := service.NewQuoteService(repo, pool, 30*time.Second)
		return svc, repo, pool
	}

	t.Run("creates new quote request successfully", func(t *testing.T) {
		svc, _, pool := setupService()
		defer pool.Stop()

		req, isNew, err := svc.RequestQuoteUpdate(ctx, "EUR/MXN", "")
		require.NoError(t, err)
		assert.True(t, isNew)
		assert.NotEqual(t, uuid.Nil, req.ID)
		assert.Equal(t, "EUR/MXN", req.Currency)
		assert.Equal(t, domain.StatusPending, req.Status)
	})

	t.Run("idempotency key returns identical request without duplicate processing", func(t *testing.T) {
		svc, _, pool := setupService()
		defer pool.Stop()

		key := "client-tx-12345"

		// First request
		req1, isNew1, err := svc.RequestQuoteUpdate(ctx, "EUR/MXN", key)
		require.NoError(t, err)
		assert.True(t, isNew1)

		// Second request with SAME key and SAME currency
		req2, isNew2, err := svc.RequestQuoteUpdate(ctx, "EUR/MXN", key)
		require.NoError(t, err)
		assert.False(t, isNew2)
		assert.Equal(t, req1.ID, req2.ID)
	})

	t.Run("idempotency key conflict with different payload returns error", func(t *testing.T) {
		svc, _, pool := setupService()
		defer pool.Stop()

		key := "client-tx-conflict"

		// First request with EUR/MXN
		_, _, err := svc.RequestQuoteUpdate(ctx, "EUR/MXN", key)
		require.NoError(t, err)

		// Second request with SAME key but DIFFERENT currency USD/MXN
		_, _, err = svc.RequestQuoteUpdate(ctx, "USD/MXN", key)
		require.Error(t, err)
		assert.ErrorIs(t, err, domain.ErrDuplicateIdempotencyKey)
	})

	t.Run("in-flight deduplication returns existing pending request", func(t *testing.T) {
		svc, _, pool := setupService()
		defer pool.Stop()

		// First request without key
		req1, isNew1, err := svc.RequestQuoteUpdate(ctx, "USD/MXN", "")
		require.NoError(t, err)
		assert.True(t, isNew1)

		// Immediate second request for same pair without key -> should deduplicate
		req2, isNew2, err := svc.RequestQuoteUpdate(ctx, "USD/MXN", "")
		require.NoError(t, err)
		assert.False(t, isNew2)
		assert.Equal(t, req1.ID, req2.ID)
	})

	t.Run("invalid currency returns error", func(t *testing.T) {
		svc, _, pool := setupService()
		defer pool.Stop()

		_, _, err := svc.RequestQuoteUpdate(ctx, "INVALID", "")
		require.Error(t, err)
		assert.ErrorIs(t, err, domain.ErrInvalidCurrencyPair)
	})

	t.Run("get quote by ID", func(t *testing.T) {
		svc, _, pool := setupService()
		defer pool.Stop()

		req, _, err := svc.RequestQuoteUpdate(ctx, "EUR/USD", "")
		require.NoError(t, err)

		fetched, err := svc.GetQuoteByID(ctx, req.ID)
		require.NoError(t, err)
		assert.Equal(t, req.ID, fetched.ID)

		// Non-existent ID
		_, err = svc.GetQuoteByID(ctx, uuid.New())
		require.Error(t, err)
		assert.ErrorIs(t, err, domain.ErrQuoteRequestNotFound)
	})

	t.Run("get latest quote", func(t *testing.T) {
		svc, repo, pool := setupService()
		defer pool.Stop()

		// Non-existent before update
		_, err := svc.GetLatestQuote(ctx, "EUR/MXN")
		require.Error(t, err)
		assert.ErrorIs(t, err, domain.ErrLatestQuoteNotFound)

		// Complete a quote manually in repo
		reqID := uuid.New()
		require.NoError(t, repo.CreateRequest(ctx, &domain.QuoteRequest{
			ID:       reqID,
			Currency: "EUR/MXN",
			Status:   domain.StatusPending,
		}))
		require.NoError(t, repo.CompleteRequestAndSaveLatest(ctx, reqID, "EUR/MXN", 20.447892))

		latest, err := svc.GetLatestQuote(ctx, "EUR/MXN")
		require.NoError(t, err)
		assert.Equal(t, 20.447892, latest.Price)
		assert.Equal(t, "EUR/MXN", latest.Currency)
	})
}
