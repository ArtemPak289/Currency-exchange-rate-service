package worker_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ArtemPak289/Currency-exchange-rate-service/internal/domain"
	"github.com/ArtemPak289/Currency-exchange-rate-service/internal/exchange"
	"github.com/ArtemPak289/Currency-exchange-rate-service/internal/repository/memory"
	"github.com/ArtemPak289/Currency-exchange-rate-service/internal/worker"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkerPool(t *testing.T) {
	ctx := context.Background()

	t.Run("successfully processes job and updates status", func(t *testing.T) {
		repo := memory.NewRepository()
		mockFetcher := exchange.NewMockFetcher()
		mockFetcher.SetRate("EUR/MXN", 21.50)

		pool := worker.NewPool(repo, mockFetcher, 2, 10, 2, 10*time.Millisecond)
		pool.Start(ctx)
		defer pool.Stop()

		reqID := uuid.New()
		require.NoError(t, repo.CreateRequest(ctx, &domain.QuoteRequest{
			ID:       reqID,
			Currency: "EUR/MXN",
			Status:   domain.StatusPending,
		}))

		enqueued := pool.Enqueue(worker.Job{
			RequestID: reqID,
			Currency:  "EUR/MXN",
		})
		require.True(t, enqueued)

		// Wait for worker to finish
		assert.Eventually(t, func() bool {
			req, err := repo.GetRequestByID(ctx, reqID)
			return err == nil && req.Status == domain.StatusCompleted
		}, 2*time.Second, 20*time.Millisecond)

		req, err := repo.GetRequestByID(ctx, reqID)
		require.NoError(t, err)
		assert.Equal(t, domain.StatusCompleted, req.Status)
		require.NotNil(t, req.Price)
		assert.Equal(t, 21.50, *req.Price)

		latest, err := repo.GetLatestQuote(ctx, "EUR/MXN")
		require.NoError(t, err)
		assert.Equal(t, 21.50, latest.Price)
	})

	t.Run("handles failure and retries", func(t *testing.T) {
		repo := memory.NewRepository()
		mockFetcher := exchange.NewMockFetcher()
		mockFetcher.SetError(errors.New("network timeout"))

		pool := worker.NewPool(repo, mockFetcher, 1, 10, 1, 5*time.Millisecond)
		pool.Start(ctx)
		defer pool.Stop()

		reqID := uuid.New()
		require.NoError(t, repo.CreateRequest(ctx, &domain.QuoteRequest{
			ID:       reqID,
			Currency: "USD/MXN",
			Status:   domain.StatusPending,
		}))

		enqueued := pool.Enqueue(worker.Job{
			RequestID: reqID,
			Currency:  "USD/MXN",
		})
		require.True(t, enqueued)

		// Wait for retries to exhaust and status to become FAILED
		assert.Eventually(t, func() bool {
			req, err := repo.GetRequestByID(ctx, reqID)
			return err == nil && req.Status == domain.StatusFailed
		}, 3*time.Second, 20*time.Millisecond)

		req, err := repo.GetRequestByID(ctx, reqID)
		require.NoError(t, err)
		assert.Equal(t, domain.StatusFailed, req.Status)
		require.NotNil(t, req.ErrorMessage)
		assert.Contains(t, *req.ErrorMessage, "network timeout")
	})

	t.Run("invalid currency fails immediately without retry", func(t *testing.T) {
		repo := memory.NewRepository()
		mockFetcher := exchange.NewMockFetcher()

		pool := worker.NewPool(repo, mockFetcher, 1, 10, 3, 5*time.Millisecond)
		pool.Start(ctx)
		defer pool.Stop()

		reqID := uuid.New()
		require.NoError(t, repo.CreateRequest(ctx, &domain.QuoteRequest{
			ID:       reqID,
			Currency: "INVALID_PAIR",
			Status:   domain.StatusPending,
		}))

		pool.Enqueue(worker.Job{
			RequestID: reqID,
			Currency:  "INVALID_PAIR",
		})

		assert.Eventually(t, func() bool {
			req, err := repo.GetRequestByID(ctx, reqID)
			return err == nil && req.Status == domain.StatusFailed
		}, 2*time.Second, 20*time.Millisecond)
	})

	t.Run("pool defaults and enqueue when stopped", func(t *testing.T) {
		repo := memory.NewRepository()
		mockFetcher := exchange.NewMockFetcher()

		pool := worker.NewPool(repo, mockFetcher, 0, 0, -1, 0)
		assert.NotNil(t, pool)

		pool.Start(ctx)
		pool.Stop()

		enqueued := pool.Enqueue(worker.Job{
			RequestID: uuid.New(),
			Currency:  "EUR/MXN",
		})
		assert.False(t, enqueued, "Should return false when pool is stopped")
	})

	t.Run("startup recovery of pending requests", func(t *testing.T) {
		repo := memory.NewRepository()
		mockFetcher := exchange.NewMockFetcher()
		mockFetcher.SetRate("EUR/MXN", 21.0)

		pendingID := uuid.New()
		require.NoError(t, repo.CreateRequest(ctx, &domain.QuoteRequest{
			ID:       pendingID,
			Currency: "EUR/MXN",
			Status:   domain.StatusPending,
		}))

		pool := worker.NewPool(repo, mockFetcher, 2, 10, 2, 5*time.Millisecond)
		pool.Start(ctx)
		defer pool.Stop()

		assert.Eventually(t, func() bool {
			req, err := repo.GetRequestByID(ctx, pendingID)
			return err == nil && req.Status == domain.StatusCompleted
		}, 3*time.Second, 50*time.Millisecond)
	})
}
