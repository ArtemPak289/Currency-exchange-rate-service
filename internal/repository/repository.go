package repository

import (
	"context"
	"time"

	"github.com/ArtemPak289/Currency-exchange-rate-service/internal/domain"
	"github.com/google/uuid"
)

// QuoteRepository defines the data access contract for quote requests and latest quotes.
type QuoteRepository interface {
	// CreateRequest inserts a new quote update request.
	CreateRequest(ctx context.Context, req *domain.QuoteRequest) error

	// GetRequestByID retrieves a quote update request by its unique ID.
	GetRequestByID(ctx context.Context, id uuid.UUID) (*domain.QuoteRequest, error)

	// GetRequestByIdempotencyKey retrieves a quote update request by its idempotency key.
	GetRequestByIdempotencyKey(ctx context.Context, key string) (*domain.QuoteRequest, error)

	// FindInFlightRequest finds an active request (PENDING or PROCESSING) for currency created within window.
	FindInFlightRequest(ctx context.Context, currency string, window time.Duration) (*domain.QuoteRequest, error)

	// UpdateRequestStatus updates the status, price, and error message of a quote request.
	UpdateRequestStatus(ctx context.Context, id uuid.UUID, status domain.RequestStatus, price *float64, errMessage *string) error

	// CompleteRequestAndSaveLatest marks the request as COMPLETED and upserts latest quote atomically.
	CompleteRequestAndSaveLatest(ctx context.Context, id uuid.UUID, currency string, price float64) error

	// FailRequest marks the request as FAILED with an error explanation.
	FailRequest(ctx context.Context, id uuid.UUID, errMessage string) error

	// GetLatestQuote retrieves the most recent exchange rate for a currency pair.
	GetLatestQuote(ctx context.Context, currency string) (*domain.LatestQuote, error)

	// ListPendingRequests returns pending or interrupted requests for startup recovery.
	ListPendingRequests(ctx context.Context, limit int) ([]*domain.QuoteRequest, error)
}
