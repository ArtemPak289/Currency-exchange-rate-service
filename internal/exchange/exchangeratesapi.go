package exchange

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// APIResponse represents the JSON response structure from exchangeratesapi.io.
type APIResponse struct {
	Success   bool               `json:"success"`
	Timestamp int64              `json:"timestamp"`
	Base      string             `json:"base"`
	Date      string             `json:"date"`
	Rates     map[string]float64 `json:"rates"`
	Error     *APIError          `json:"error,omitempty"`
}

// APIError represents the error structure returned by exchangeratesapi.io.
type APIError struct {
	Code    interface{} `json:"code"`
	Message string      `json:"message"`
	Info    string      `json:"info"`
}

func (e *APIError) Error() string {
	if e == nil {
		return ""
	}
	if e.Info != "" {
		return fmt.Sprintf("%v: %s", e.Code, e.Info)
	}
	return fmt.Sprintf("%v: %s", e.Code, e.Message)
}

// ExchangeRatesAPIClient implements Fetcher using the exchangeratesapi.io service.
type ExchangeRatesAPIClient struct {
	apiKey     string
	baseURL    string
	httpClient *http.Client
	cacheTTL   time.Duration

	// In-memory rate cache
	mu       sync.RWMutex
	cache    map[string]float64
	cachedAt time.Time
}

// NewExchangeRatesAPIClient creates a new client for exchangeratesapi.io.
func NewExchangeRatesAPIClient(apiKey, baseURL string, timeout, cacheTTL time.Duration) *ExchangeRatesAPIClient {
	if baseURL == "" {
		baseURL = "http://api.exchangeratesapi.io/v1/latest"
	}
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	if cacheTTL <= 0 {
		cacheTTL = 30 * time.Second
	}

	return &ExchangeRatesAPIClient{
		apiKey:  apiKey,
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: timeout,
		},
		cacheTTL: cacheTTL,
		cache:    make(map[string]float64),
	}
}

// FetchRate returns the exchange rate for 1 base in terms of quote (e.g. base=EUR, quote=MXN -> 20.44).
func (c *ExchangeRatesAPIClient) FetchRate(ctx context.Context, base, quote string) (float64, error) {
	base = strings.ToUpper(strings.TrimSpace(base))
	quote = strings.ToUpper(strings.TrimSpace(quote))

	if base == quote {
		return 1.0, nil
	}

	// 1. Check cache first
	if rate, ok := c.getCachedRate(base, quote); ok {
		return rate, nil
	}

	// 2. Fetch fresh rates from external API
	if err := c.refreshRates(ctx, base, quote); err != nil {
		return 0, err
	}

	// 3. Retrieve calculated rate from updated cache
	if rate, ok := c.getCachedRate(base, quote); ok {
		return rate, nil
	}

	return 0, fmt.Errorf("%w: base=%s or quote=%s", ErrCurrencyNotFound, base, quote)
}

func (c *ExchangeRatesAPIClient) getCachedRate(base, quote string) (float64, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	if time.Since(c.cachedAt) > c.cacheTTL {
		return 0, false
	}

	rateBase, okBase := c.cache[base]
	rateQuote, okQuote := c.cache[quote]

	// EUR is the standard base currency (1.0) on exchangeratesapi.io free tier
	if base == "EUR" {
		rateBase = 1.0
		okBase = true
	}
	if quote == "EUR" {
		rateQuote = 1.0
		okQuote = true
	}

	if !okBase || !okQuote || rateBase <= 0 {
		return 0, false
	}

	// Calculate cross-rate: (EUR -> Quote) / (EUR -> Base)
	rawRate := rateQuote / rateBase
	return roundRate(rawRate, 6), true
}

func (c *ExchangeRatesAPIClient) refreshRates(ctx context.Context, requestedCurrencies ...string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Double-check if another goroutine just refreshed the cache
	if time.Since(c.cachedAt) <= c.cacheTTL {
		allFound := true
		for _, cur := range requestedCurrencies {
			if cur != "EUR" {
				if _, ok := c.cache[cur]; !ok {
					allFound = false
					break
				}
			}
		}
		if allFound {
			return nil
		}
	}

	// Build symbols list: USD, EUR, MXN, UZS + requested currencies
	symbolSet := map[string]struct{}{
		"USD": {},
		"EUR": {},
		"MXN": {},
		"UZS": {},
	}
	for _, cur := range requestedCurrencies {
		if cur != "" && cur != "EUR" {
			symbolSet[cur] = struct{}{}
		}
	}

	symbolsList := make([]string, 0, len(symbolSet))
	for sym := range symbolSet {
		symbolsList = append(symbolsList, sym)
	}

	reqURL, err := url.Parse(c.baseURL)
	if err != nil {
		return fmt.Errorf("invalid base URL: %w", err)
	}

	q := reqURL.Query()
	q.Set("access_key", c.apiKey)
	q.Set("symbols", strings.Join(symbolsList, ","))
	reqURL.RawQuery = q.Encode()

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL.String(), nil)
	if err != nil {
		return fmt.Errorf("failed to create http request: %w", err)
	}

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("external exchange API request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read API response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("external API responded with status %d: %s", resp.StatusCode, string(body))
	}

	var apiResp APIResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return fmt.Errorf("failed to decode API response JSON: %w", err)
	}

	if !apiResp.Success {
		if apiResp.Error != nil {
			return fmt.Errorf("%w: %s", ErrAPIResponse, apiResp.Error.Error())
		}
		return fmt.Errorf("%w: unknown error from provider", ErrAPIResponse)
	}

	if apiResp.Rates == nil {
		return fmt.Errorf("%w: rates map missing in response", ErrAPIResponse)
	}

	// Update cache
	for k, v := range apiResp.Rates {
		c.cache[k] = v
	}
	c.cache["EUR"] = 1.0
	c.cachedAt = time.Now()

	return nil
}

func roundRate(val float64, decimals int) float64 {
	pow := math.Pow(10, float64(decimals))
	return math.Round(val*pow) / pow
}
