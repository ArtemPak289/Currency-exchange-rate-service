package http

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/ArtemPak289/Currency-exchange-rate-service/internal/domain"
	"github.com/ArtemPak289/Currency-exchange-rate-service/internal/service"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// UpdateQuoteRequest is the incoming payload to request an asynchronous quote refresh.
type UpdateQuoteRequest struct {
	Currency string `json:"currency"`
}

// UpdateQuoteResponse is returned when a quote update is successfully scheduled or deduplicated.
type UpdateQuoteResponse struct {
	ID        uuid.UUID            `json:"id"`
	Currency  string               `json:"currency"`
	Status    domain.RequestStatus `json:"status"`
	CreatedAt time.Time            `json:"created_at"`
}

// QuoteDetailResponse is returned when querying a quote update by its ID.
type QuoteDetailResponse struct {
	ID           uuid.UUID            `json:"id"`
	Currency     string               `json:"currency"`
	Status       domain.RequestStatus `json:"status"`
	Price        *float64             `json:"price,omitempty"`
	ErrorMessage *string              `json:"error,omitempty"`
	CreatedAt    time.Time            `json:"created_at"`
	UpdatedAt    time.Time            `json:"updated_at"`
}

// LatestQuoteResponse is returned when querying the latest quote for a currency.
type LatestQuoteResponse struct {
	Currency  string    `json:"currency"`
	Price     float64   `json:"price"`
	UpdatedAt time.Time `json:"updated_at"`
}

// QuoteHandler handles HTTP endpoints for currency quote updates and lookups.
type QuoteHandler struct {
	service *service.QuoteService
}

// NewQuoteHandler creates a new QuoteHandler instance.
func NewQuoteHandler(svc *service.QuoteService) *QuoteHandler {
	return &QuoteHandler{service: svc}
}

// RefreshQuote handles POST /api/v1/quotes (and /api/v1/quotes/refresh).
// Triggers an asynchronous quote update without blocking the HTTP handler.
func (h *QuoteHandler) RefreshQuote(w http.ResponseWriter, r *http.Request) {
	var req UpdateQuoteRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		Error(w, http.StatusBadRequest, "invalid request body: expected JSON with 'currency' field")
		return
	}

	if strings.TrimSpace(req.Currency) == "" {
		Error(w, http.StatusBadRequest, "field 'currency' is required (e.g. 'EUR/MXN')")
		return
	}

	idempotencyKey := r.Header.Get("Idempotency-Key")

	quoteReq, isNew, err := h.service.RequestQuoteUpdate(r.Context(), req.Currency, idempotencyKey)
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrInvalidCurrencyPair), errors.Is(err, domain.ErrIdenticalCurrencies):
			Error(w, http.StatusBadRequest, err.Error())
		case errors.Is(err, domain.ErrDuplicateIdempotencyKey):
			Error(w, http.StatusConflict, err.Error())
		case errors.Is(err, domain.ErrQueueFull):
			Error(w, http.StatusServiceUnavailable, err.Error())
		default:
			Error(w, http.StatusInternalServerError, "failed to schedule quote update: "+err.Error())
		}
		return
	}

	statusCode := http.StatusAccepted
	if !isNew {
		w.Header().Set("Idempotent-Replayed", "true")
		if quoteReq.Status == domain.StatusCompleted {
			statusCode = http.StatusOK
		}
	}

	JSON(w, statusCode, UpdateQuoteResponse{
		ID:        quoteReq.ID,
		Currency:  quoteReq.Currency,
		Status:    quoteReq.Status,
		CreatedAt: quoteReq.CreatedAt,
	})
}

// GetQuoteByID handles GET /api/v1/quotes/{id}.
// Returns the status, price, and updated_at timestamp for a given update request.
func (h *QuoteHandler) GetQuoteByID(w http.ResponseWriter, r *http.Request) {
	idParam := chi.URLParam(r, "id")
	if idParam == "" {
		Error(w, http.StatusBadRequest, "missing quote request ID")
		return
	}

	id, err := uuid.Parse(idParam)
	if err != nil {
		Error(w, http.StatusBadRequest, "invalid UUID format for quote request ID")
		return
	}

	quoteReq, err := h.service.GetQuoteByID(r.Context(), id)
	if err != nil {
		if errors.Is(err, domain.ErrQuoteRequestNotFound) {
			Error(w, http.StatusNotFound, "quote request with specified ID was not found")
			return
		}
		Error(w, http.StatusInternalServerError, "failed to retrieve quote request: "+err.Error())
		return
	}

	JSON(w, http.StatusOK, QuoteDetailResponse{
		ID:           quoteReq.ID,
		Currency:     quoteReq.Currency,
		Status:       quoteReq.Status,
		Price:        quoteReq.Price,
		ErrorMessage: quoteReq.ErrorMessage,
		CreatedAt:    quoteReq.CreatedAt,
		UpdatedAt:    quoteReq.UpdatedAt,
	})
}

// GetLatestQuote handles GET /api/v1/quotes/latest?currency=EUR/MXN
// Also supports GET /api/v1/quotes/latest/{currency...}
func (h *QuoteHandler) GetLatestQuote(w http.ResponseWriter, r *http.Request) {
	currency := r.URL.Query().Get("currency")
	if currency == "" {
		currency = r.URL.Query().Get("symbol")
	}
	if currency == "" {
		currency = chi.URLParam(r, "currency")
	}

	if strings.TrimSpace(currency) == "" {
		Error(w, http.StatusBadRequest, "query parameter 'currency' is required (e.g. ?currency=EUR/MXN)")
		return
	}

	latest, err := h.service.GetLatestQuote(r.Context(), currency)
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrInvalidCurrencyPair), errors.Is(err, domain.ErrIdenticalCurrencies):
			Error(w, http.StatusBadRequest, err.Error())
		case errors.Is(err, domain.ErrLatestQuoteNotFound):
			Error(w, http.StatusNotFound, "no quotes found for currency "+currency)
		default:
			Error(w, http.StatusInternalServerError, "failed to retrieve latest quote: "+err.Error())
		}
		return
	}

	JSON(w, http.StatusOK, LatestQuoteResponse{
		Currency:  latest.Currency,
		Price:     latest.Price,
		UpdatedAt: latest.UpdatedAt,
	})
}
