package memory

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/ArtemPak289/Currency-exchange-rate-service/internal/domain"
	"github.com/google/uuid"
)

// Repository is a thread-safe in-memory implementation of repository.QuoteRepository.
type Repository struct {
	mu           sync.RWMutex
	requests     map[uuid.UUID]*domain.QuoteRequest
	idempotency  map[string]uuid.UUID
	latestQuotes map[string]*domain.LatestQuote
}

// NewRepository creates a new in-memory repository.
func NewRepository() *Repository {
	return &Repository{
		requests:     make(map[uuid.UUID]*domain.QuoteRequest),
		idempotency:  make(map[string]uuid.UUID),
		latestQuotes: make(map[string]*domain.LatestQuote),
	}
}

func (r *Repository) CreateRequest(ctx context.Context, req *domain.QuoteRequest) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if req.ID == uuid.Nil {
		req.ID = uuid.New()
	}
	now := time.Now().UTC()
	if req.CreatedAt.IsZero() {
		req.CreatedAt = now
	}
	req.UpdatedAt = now

	// Clone to prevent external mutation
	cloned := *req
	r.requests[req.ID] = &cloned

	if req.IdempotencyKey != nil && *req.IdempotencyKey != "" {
		r.idempotency[*req.IdempotencyKey] = req.ID
	}

	return nil
}

func (r *Repository) GetRequestByID(ctx context.Context, id uuid.UUID) (*domain.QuoteRequest, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	req, ok := r.requests[id]
	if !ok {
		return nil, domain.ErrQuoteRequestNotFound
	}

	cloned := *req
	return &cloned, nil
}

func (r *Repository) GetRequestByIdempotencyKey(ctx context.Context, key string) (*domain.QuoteRequest, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	id, ok := r.idempotency[key]
	if !ok {
		return nil, domain.ErrQuoteRequestNotFound
	}

	req, ok := r.requests[id]
	if !ok {
		return nil, domain.ErrQuoteRequestNotFound
	}

	cloned := *req
	return &cloned, nil
}

func (r *Repository) FindInFlightRequest(ctx context.Context, currency string, window time.Duration) (*domain.QuoteRequest, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	normalized := strings.ToUpper(strings.TrimSpace(currency))
	now := time.Now().UTC()

	var mostRecent *domain.QuoteRequest
	for _, req := range r.requests {
		if req.Currency == normalized && (req.Status == domain.StatusPending || req.Status == domain.StatusProcessing) {
			if window > 0 && now.Sub(req.CreatedAt) > window {
				continue
			}
			if mostRecent == nil || req.CreatedAt.After(mostRecent.CreatedAt) {
				mostRecent = req
			}
		}
	}

	if mostRecent == nil {
		return nil, domain.ErrQuoteRequestNotFound
	}

	cloned := *mostRecent
	return &cloned, nil
}

func (r *Repository) UpdateRequestStatus(ctx context.Context, id uuid.UUID, status domain.RequestStatus, price *float64, errMessage *string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	req, ok := r.requests[id]
	if !ok {
		return domain.ErrQuoteRequestNotFound
	}

	req.Status = status
	req.UpdatedAt = time.Now().UTC()
	if price != nil {
		req.Price = price
	}
	if errMessage != nil {
		req.ErrorMessage = errMessage
	}

	return nil
}

func (r *Repository) CompleteRequestAndSaveLatest(ctx context.Context, id uuid.UUID, currency string, price float64) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	req, ok := r.requests[id]
	if !ok {
		return domain.ErrQuoteRequestNotFound
	}

	now := time.Now().UTC()
	req.Status = domain.StatusCompleted
	req.Price = &price
	req.ErrorMessage = nil
	req.UpdatedAt = now

	normalized := strings.ToUpper(strings.TrimSpace(currency))
	r.latestQuotes[normalized] = &domain.LatestQuote{
		Currency:       normalized,
		Price:          price,
		QuoteRequestID: &id,
		UpdatedAt:      now,
	}

	return nil
}

func (r *Repository) FailRequest(ctx context.Context, id uuid.UUID, errMessage string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	req, ok := r.requests[id]
	if !ok {
		return domain.ErrQuoteRequestNotFound
	}

	now := time.Now().UTC()
	req.Status = domain.StatusFailed
	req.ErrorMessage = &errMessage
	req.UpdatedAt = now

	return nil
}

func (r *Repository) SaveLatestQuote(ctx context.Context, quote *domain.LatestQuote) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	normalized := strings.ToUpper(strings.TrimSpace(quote.Currency))
	now := time.Now().UTC()
	if quote.UpdatedAt.IsZero() {
		quote.UpdatedAt = now
	}

	cloned := *quote
	cloned.Currency = normalized
	r.latestQuotes[normalized] = &cloned
	return nil
}

func (r *Repository) GetLatestQuote(ctx context.Context, currency string) (*domain.LatestQuote, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	normalized := strings.ToUpper(strings.TrimSpace(currency))
	quote, ok := r.latestQuotes[normalized]
	if !ok {
		return nil, domain.ErrLatestQuoteNotFound
	}

	cloned := *quote
	return &cloned, nil
}

func (r *Repository) ListPendingRequests(ctx context.Context, limit int) ([]*domain.QuoteRequest, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []*domain.QuoteRequest
	for _, req := range r.requests {
		if req.Status == domain.StatusPending || req.Status == domain.StatusProcessing {
			cloned := *req
			result = append(result, &cloned)
			if limit > 0 && len(result) >= limit {
				break
			}
		}
	}

	return result, nil
}
