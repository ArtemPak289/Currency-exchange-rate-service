package domain

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// RequestStatus represents the current processing state of a quote update request.
type RequestStatus string

const (
	StatusPending    RequestStatus = "PENDING"
	StatusProcessing RequestStatus = "PROCESSING"
	StatusCompleted  RequestStatus = "COMPLETED"
	StatusFailed     RequestStatus = "FAILED"
)

// Common domain errors.
var (
	ErrInvalidCurrencyPair     = errors.New("invalid currency pair format, expected format BASE/QUOTE (e.g., EUR/MXN)")
	ErrUnsupportedCurrency     = errors.New("unsupported currency code")
	ErrIdenticalCurrencies     = errors.New("base and quote currencies cannot be identical")
	ErrQuoteRequestNotFound    = errors.New("quote update request not found")
	ErrLatestQuoteNotFound     = errors.New("latest quote not found for specified currency")
	ErrDuplicateIdempotencyKey = errors.New("idempotency key was previously used with a different request payload")
	ErrQueueFull               = errors.New("worker queue is full, please try again later")
	ErrExternalAPIUnavailable  = errors.New("external currency rate provider is unavailable")
)

// CurrencyPair represents a normalized foreign exchange currency pair (e.g., EUR/MXN).
type CurrencyPair struct {
	Base  string `json:"base"`
	Quote string `json:"quote"`
}

// String returns the normalized canonical representation of the currency pair (e.g. "EUR/MXN").
func (cp CurrencyPair) String() string {
	return fmt.Sprintf("%s/%s", cp.Base, cp.Quote)
}

// SupportedCurrencies maps supported 3-letter currency codes.
// While the service focuses on USD, EUR, and MXN, standard ISO-4217 currencies are recognized.
var SupportedCurrencies = map[string]string{
	"USD": "United States Dollar",
	"EUR": "Euro",
	"MXN": "Mexican Peso",
	"GBP": "British Pound",
	"JPY": "Japanese Yen",
	"CAD": "Canadian Dollar",
	"CHF": "Swiss Franc",
	"AUD": "Australian Dollar",
	"BRL": "Brazilian Real",
	"CNY": "Chinese Yuan",
}

// ParseCurrencyPair parses and normalizes currency input strings.
// Supports delimiters "/" and "-", as well as concatenated 6-letter formats (e.g. "EURMXN", "eur/mxn").
func ParseCurrencyPair(input string) (CurrencyPair, error) {
	cleaned := strings.TrimSpace(strings.ToUpper(input))
	if cleaned == "" {
		return CurrencyPair{}, ErrInvalidCurrencyPair
	}

	var base, quote string
	if strings.Contains(cleaned, "/") {
		parts := strings.Split(cleaned, "/")
		if len(parts) != 2 {
			return CurrencyPair{}, ErrInvalidCurrencyPair
		}
		base = strings.TrimSpace(parts[0])
		quote = strings.TrimSpace(parts[1])
	} else if strings.Contains(cleaned, "-") {
		parts := strings.Split(cleaned, "-")
		if len(parts) != 2 {
			return CurrencyPair{}, ErrInvalidCurrencyPair
		}
		base = strings.TrimSpace(parts[0])
		quote = strings.TrimSpace(parts[1])
	} else if len(cleaned) == 6 {
		base = cleaned[:3]
		quote = cleaned[3:]
	} else {
		return CurrencyPair{}, ErrInvalidCurrencyPair
	}

	if len(base) != 3 || len(quote) != 3 {
		return CurrencyPair{}, ErrInvalidCurrencyPair
	}

	// Verify alphabetic characters
	for _, r := range base {
		if r < 'A' || r > 'Z' {
			return CurrencyPair{}, ErrInvalidCurrencyPair
		}
	}
	for _, r := range quote {
		if r < 'A' || r > 'Z' {
			return CurrencyPair{}, ErrInvalidCurrencyPair
		}
	}

	if base == quote {
		return CurrencyPair{}, ErrIdenticalCurrencies
	}

	return CurrencyPair{
		Base:  base,
		Quote: quote,
	}, nil
}

// QuoteRequest represents an asynchronous request to update a currency exchange quote.
type QuoteRequest struct {
	ID             uuid.UUID     `json:"id"`
	Currency       string        `json:"currency"`
	Status         RequestStatus `json:"status"`
	Price          *float64      `json:"price,omitempty"`
	ErrorMessage   *string       `json:"error,omitempty"`
	IdempotencyKey *string       `json:"idempotency_key,omitempty"`
	CreatedAt      time.Time     `json:"created_at"`
	UpdatedAt      time.Time     `json:"updated_at"`
}

// LatestQuote represents the most recently fetched price for a currency pair.
type LatestQuote struct {
	Currency       string     `json:"currency"`
	Price          float64    `json:"price"`
	QuoteRequestID *uuid.UUID `json:"quote_request_id,omitempty"`
	UpdatedAt      time.Time  `json:"updated_at"`
}
