package exchange

import (
	"context"
	"fmt"
	"strings"
	"sync"
)

// MockFetcher provides a thread-safe in-memory rate fetcher for testing and offline development.
type MockFetcher struct {
	mu     sync.RWMutex
	rates  map[string]float64
	err    error
	callMu sync.Mutex
	calls  []string
}

// NewMockFetcher creates a new MockFetcher populated with realistic default rates.
func NewMockFetcher() *MockFetcher {
	return &MockFetcher{
		rates: map[string]float64{
			"EUR/MXN": 20.447892,
			"MXN/EUR": 0.048905,
			"USD/MXN": 18.160810,
			"MXN/USD": 0.055064,
			"EUR/USD": 1.125935,
			"USD/EUR": 0.888151,
			"EUR/GBP": 0.855000,
			"GBP/EUR": 1.169591,
		},
		calls: make([]string, 0),
	}
}

// SetRate sets a specific rate for a pair.
func (m *MockFetcher) SetRate(pair string, rate float64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rates[strings.ToUpper(strings.TrimSpace(pair))] = rate
}

// SetError forces FetchRate to return the specified error.
func (m *MockFetcher) SetError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.err = err
}

// FetchRate returns the mocked rate or calculates inverse / cross-rate if available.
func (m *MockFetcher) FetchRate(ctx context.Context, base, quote string) (float64, error) {
	m.callMu.Lock()
	m.calls = append(m.calls, fmt.Sprintf("%s/%s", base, quote))
	m.callMu.Unlock()

	m.mu.RLock()
	defer m.mu.RUnlock()

	if m.err != nil {
		return 0, m.err
	}

	base = strings.ToUpper(strings.TrimSpace(base))
	quote = strings.ToUpper(strings.TrimSpace(quote))

	if base == quote {
		return 1.0, nil
	}

	pair := fmt.Sprintf("%s/%s", base, quote)
	if rate, ok := m.rates[pair]; ok {
		return rate, nil
	}

	// Try inverse
	inversePair := fmt.Sprintf("%s/%s", quote, base)
	if rate, ok := m.rates[inversePair]; ok && rate > 0 {
		return roundRate(1.0/rate, 6), nil
	}

	return 0, fmt.Errorf("%w: pair=%s", ErrCurrencyNotFound, pair)
}

// Calls returns the list of pairs requested.
func (m *MockFetcher) Calls() []string {
	m.callMu.Lock()
	defer m.callMu.Unlock()
	copied := make([]string, len(m.calls))
	copy(copied, m.calls)
	return copied
}
