package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ArtemPak289/Currency-exchange-rate-service/internal/domain"
	"github.com/ArtemPak289/Currency-exchange-rate-service/internal/repository"
	"github.com/ArtemPak289/Currency-exchange-rate-service/internal/worker"
	"github.com/google/uuid"
)

// QuoteService handles business logic for quote update requests and queries.
type QuoteService struct {
	repo        repository.QuoteRepository
	workerPool  *worker.Pool
	dedupWindow time.Duration
}

// NewQuoteService creates a new QuoteService instance.
func NewQuoteService(repo repository.QuoteRepository, pool *worker.Pool, dedupWindow time.Duration) *QuoteService {
	return &QuoteService{
		repo:        repo,
		workerPool:  pool,
		dedupWindow: dedupWindow,
	}
}

// RequestQuoteUpdate initiates an asynchronous quote update.
// It enforces idempotency and in-flight deduplication.
// Returns the QuoteRequest, a boolean indicating if this is a newly created request, and any error.
func (s *QuoteService) RequestQuoteUpdate(ctx context.Context, currencyInput, idempotencyKey string) (*domain.QuoteRequest, bool, error) {
	// 1. Validate and normalize currency pair
	pair, err := domain.ParseCurrencyPair(currencyInput)
	if err != nil {
		return nil, false, err
	}

	normalizedCurrency := pair.String()
	trimmedKey := strings.TrimSpace(idempotencyKey)

	// 2. Check Idempotency Key if provided
	if trimmedKey != "" {
		existing, err := s.repo.GetRequestByIdempotencyKey(ctx, trimmedKey)
		if err == nil && existing != nil {
			// Idempotency key already exists. Verify payload matches.
			if existing.Currency != normalizedCurrency {
				return nil, false, fmt.Errorf("%w: key %q was already used for currency %s",
					domain.ErrDuplicateIdempotencyKey, trimmedKey, existing.Currency)
			}
			// Exact duplicate: return existing request
			return existing, false, nil
		}
	}

	// 3. Deduplicate in-flight requests (if no explicit idempotency key was given)
	if trimmedKey == "" && s.dedupWindow > 0 {
		inFlight, err := s.repo.FindInFlightRequest(ctx, normalizedCurrency, s.dedupWindow)
		if err == nil && inFlight != nil {
			return inFlight, false, nil
		}
	}

	// 4. Create new QuoteRequest
	reqID := uuid.New()
	var keyPtr *string
	if trimmedKey != "" {
		keyPtr = &trimmedKey
	}

	newReq := &domain.QuoteRequest{
		ID:             reqID,
		Currency:       normalizedCurrency,
		Status:         domain.StatusPending,
		IdempotencyKey: keyPtr,
		CreatedAt:      time.Now().UTC(),
		UpdatedAt:      time.Now().UTC(),
	}

	// 5. Persist to database
	if err := s.repo.CreateRequest(ctx, newReq); err != nil {
		return nil, false, fmt.Errorf("failed to save quote request: %w", err)
	}

	// 6. Enqueue job to background worker
	enqueued := s.workerPool.Enqueue(worker.Job{
		RequestID:  reqID,
		Currency:   normalizedCurrency,
		RetryCount: 0,
	})

	if !enqueued {
		_ = s.repo.FailRequest(ctx, reqID, "background worker queue is full")
		return nil, false, domain.ErrQueueFull
	}

	return newReq, true, nil
}

// GetQuoteByID retrieves a quote update request by its identifier.
func (s *QuoteService) GetQuoteByID(ctx context.Context, id uuid.UUID) (*domain.QuoteRequest, error) {
	if id == uuid.Nil {
		return nil, domain.ErrQuoteRequestNotFound
	}
	return s.repo.GetRequestByID(ctx, id)
}

// GetLatestQuote retrieves the most recently fetched quote for a currency.
func (s *QuoteService) GetLatestQuote(ctx context.Context, currencyInput string) (*domain.LatestQuote, error) {
	pair, err := domain.ParseCurrencyPair(currencyInput)
	if err != nil {
		return nil, err
	}
	return s.repo.GetLatestQuote(ctx, pair.String())
}
