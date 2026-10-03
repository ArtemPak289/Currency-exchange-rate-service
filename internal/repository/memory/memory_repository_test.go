package memory_test

import (
	"context"
	"testing"
	"time"

	"github.com/ArtemPak289/Currency-exchange-rate-service/internal/domain"
	"github.com/ArtemPak289/Currency-exchange-rate-service/internal/repository/memory"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMemoryRepository(t *testing.T) {
	ctx := context.Background()
	repo := memory.NewRepository()

	id := uuid.New()
	idempKey := "idemp-key-1"
	req := &domain.QuoteRequest{
		ID:             id,
		Currency:       "EUR/MXN",
		Status:         domain.StatusPending,
		IdempotencyKey: &idempKey,
	}

	t.Run("CreateRequest and GetRequestByID", func(t *testing.T) {
		err := repo.CreateRequest(ctx, req)
		require.NoError(t, err)

		fetched, err := repo.GetRequestByID(ctx, id)
		require.NoError(t, err)
		assert.Equal(t, id, fetched.ID)
		assert.Equal(t, "EUR/MXN", fetched.Currency)
		assert.Equal(t, domain.StatusPending, fetched.Status)
		assert.Equal(t, idempKey, *fetched.IdempotencyKey)
	})

	t.Run("GetRequestByIdempotencyKey", func(t *testing.T) {
		fetched, err := repo.GetRequestByIdempotencyKey(ctx, idempKey)
		require.NoError(t, err)
		assert.Equal(t, id, fetched.ID)

		_, err = repo.GetRequestByIdempotencyKey(ctx, "non-existent")
		require.Error(t, err)
		assert.ErrorIs(t, err, domain.ErrQuoteRequestNotFound)
	})

	t.Run("FindInFlightRequest", func(t *testing.T) {
		inFlight, err := repo.FindInFlightRequest(ctx, "EUR/MXN", 1*time.Minute)
		require.NoError(t, err)
		assert.Equal(t, id, inFlight.ID)

		// Currency with no in-flight requests
		_, err = repo.FindInFlightRequest(ctx, "USD/CAD", 1*time.Minute)
		require.Error(t, err)
		assert.ErrorIs(t, err, domain.ErrQuoteRequestNotFound)
	})

	t.Run("CompleteRequestAndSaveLatest", func(t *testing.T) {
		price := 20.447892
		err := repo.CompleteRequestAndSaveLatest(ctx, id, "EUR/MXN", price)
		require.NoError(t, err)

		// Request is now COMPLETED
		updatedReq, err := repo.GetRequestByID(ctx, id)
		require.NoError(t, err)
		assert.Equal(t, domain.StatusCompleted, updatedReq.Status)
		require.NotNil(t, updatedReq.Price)
		assert.Equal(t, price, *updatedReq.Price)

		// Latest quote is now available
		latest, err := repo.GetLatestQuote(ctx, "EUR/MXN")
		require.NoError(t, err)
		assert.Equal(t, "EUR/MXN", latest.Currency)
		assert.Equal(t, price, latest.Price)
		assert.Equal(t, id, *latest.QuoteRequestID)

		// Once completed, it shouldn't show as in-flight
		_, err = repo.FindInFlightRequest(ctx, "EUR/MXN", 1*time.Minute)
		require.Error(t, err)
	})

	t.Run("FailRequest", func(t *testing.T) {
		failID := uuid.New()
		failReq := &domain.QuoteRequest{
			ID:       failID,
			Currency: "USD/JPY",
			Status:   domain.StatusPending,
		}
		require.NoError(t, repo.CreateRequest(ctx, failReq))

		err := repo.FailRequest(ctx, failID, "provider timeout")
		require.NoError(t, err)

		fetched, err := repo.GetRequestByID(ctx, failID)
		require.NoError(t, err)
		assert.Equal(t, domain.StatusFailed, fetched.Status)
		require.NotNil(t, fetched.ErrorMessage)
		assert.Equal(t, "provider timeout", *fetched.ErrorMessage)
	})

	t.Run("ListPendingRequests", func(t *testing.T) {
		p1 := &domain.QuoteRequest{ID: uuid.New(), Currency: "EUR/USD", Status: domain.StatusPending}
		p2 := &domain.QuoteRequest{ID: uuid.New(), Currency: "GBP/USD", Status: domain.StatusProcessing}
		require.NoError(t, repo.CreateRequest(ctx, p1))
		require.NoError(t, repo.CreateRequest(ctx, p2))

		pending, err := repo.ListPendingRequests(ctx, 10)
		require.NoError(t, err)
		assert.GreaterOrEqual(t, len(pending), 2)
	})
}
