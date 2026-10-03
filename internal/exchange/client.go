package exchange

import (
	"context"
	"errors"
)

var (
	ErrCurrencyNotFound = errors.New("currency not found in exchange rate provider response")
	ErrAPIResponse      = errors.New("exchange rate API returned an error")
)

// Fetcher defines the contract for retrieving foreign exchange rates.
type Fetcher interface {
	// FetchRate retrieves the exchange rate for base/quote (e.g., 1 BASE = X QUOTE).
	FetchRate(ctx context.Context, base, quote string) (float64, error)
}
