package exchange_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/ArtemPak289/Currency-exchange-rate-service/internal/exchange"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMockFetcher(t *testing.T) {
	ctx := context.Background()
	mock := exchange.NewMockFetcher()

	t.Run("known direct rate", func(t *testing.T) {
		rate, err := mock.FetchRate(ctx, "EUR", "MXN")
		require.NoError(t, err)
		assert.Equal(t, 20.447892, rate)
	})

	t.Run("identical currency returns 1.0", func(t *testing.T) {
		rate, err := mock.FetchRate(ctx, "EUR", "EUR")
		require.NoError(t, err)
		assert.Equal(t, 1.0, rate)
	})

	t.Run("unknown currency returns error", func(t *testing.T) {
		_, err := mock.FetchRate(ctx, "XYZ", "ABC")
		require.Error(t, err)
		assert.ErrorIs(t, err, exchange.ErrCurrencyNotFound)
	})

	t.Run("forced error", func(t *testing.T) {
		forcedErr := errors.New("upstream provider error")
		mock.SetError(forcedErr)
		_, err := mock.FetchRate(ctx, "EUR", "MXN")
		require.Error(t, err)
		assert.Equal(t, forcedErr, err)
	})
}

func TestExchangeRatesAPIClient(t *testing.T) {
	ctx := context.Background()

	t.Run("successful rate fetch and cross rate calculation", func(t *testing.T) {
		callCount := 0
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			callCount++
			assert.Equal(t, "test-key", r.URL.Query().Get("access_key"))

			resp := exchange.APIResponse{
				Success:   true,
				Timestamp: 1700000000,
				Base:      "EUR",
				Date:      "2026-10-04",
				Rates: map[string]float64{
					"USD": 1.10,
					"MXN": 22.00,
					"GBP": 0.85,
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
		}))
		defer ts.Close()

		client := exchange.NewExchangeRatesAPIClient("test-key", ts.URL, 5*time.Second, 1*time.Minute)

		// 1. Direct rate EUR -> MXN
		rateEURMXN, err := client.FetchRate(ctx, "EUR", "MXN")
		require.NoError(t, err)
		assert.Equal(t, 22.00, rateEURMXN)
		assert.Equal(t, 1, callCount)

		// 2. Cross rate USD -> MXN (22.00 / 1.10 = 20.00) - Should hit cache!
		rateUSDMXN, err := client.FetchRate(ctx, "USD", "MXN")
		require.NoError(t, err)
		assert.Equal(t, 20.00, rateUSDMXN)
		assert.Equal(t, 1, callCount, "Should use cached rates without making second HTTP call")

		// 3. Reverse rate MXN -> EUR (1.0 / 22.00 = 0.045455)
		rateMXNEUR, err := client.FetchRate(ctx, "MXN", "EUR")
		require.NoError(t, err)
		assert.Equal(t, 0.045455, rateMXNEUR)
	})

	t.Run("API error response handling", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			resp := exchange.APIResponse{
				Success: false,
				Error: &exchange.APIError{
					Code:    101,
					Message: "invalid_access_key",
					Info:    "You have not supplied a valid API Access Key.",
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
		}))
		defer ts.Close()

		client := exchange.NewExchangeRatesAPIClient("invalid-key", ts.URL, 5*time.Second, 1*time.Minute)
		_, err := client.FetchRate(ctx, "EUR", "MXN")
		require.Error(t, err)
		assert.ErrorIs(t, err, exchange.ErrAPIResponse)
	})

	t.Run("concurrent access is thread-safe", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			resp := exchange.APIResponse{
				Success:   true,
				Timestamp: 1700000000,
				Base:      "EUR",
				Date:      "2026-10-04",
				Rates: map[string]float64{
					"USD": 1.10,
					"MXN": 22.00,
				},
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(resp)
		}))
		defer ts.Close()

		client := exchange.NewExchangeRatesAPIClient("test-key", ts.URL, 5*time.Second, 100*time.Millisecond)

		var wg sync.WaitGroup
		concurrency := 20
		for i := 0; i < concurrency; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, err := client.FetchRate(ctx, "EUR", "MXN")
				assert.NoError(t, err)
				_, err = client.FetchRate(ctx, "USD", "MXN")
				assert.NoError(t, err)
			}()
		}
		wg.Wait()
	})
}
