package domain_test

import (
	"testing"

	"github.com/ArtemPak289/Currency-exchange-rate-service/internal/domain"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseCurrencyPair(t *testing.T) {
	tests := []struct {
		name          string
		input         string
		expectedBase  string
		expectedQuote string
		expectedErr   error
	}{
		{
			name:          "standard slash format uppercase",
			input:         "EUR/MXN",
			expectedBase:  "EUR",
			expectedQuote: "MXN",
			expectedErr:   nil,
		},
		{
			name:          "lowercase slash format with whitespace",
			input:         "  eur/mxn  ",
			expectedBase:  "EUR",
			expectedQuote: "MXN",
			expectedErr:   nil,
		},
		{
			name:          "hyphen delimiter",
			input:         "USD-MXN",
			expectedBase:  "USD",
			expectedQuote: "MXN",
			expectedErr:   nil,
		},
		{
			name:          "concatenated 6 characters",
			input:         "EURUSD",
			expectedBase:  "EUR",
			expectedQuote: "USD",
			expectedErr:   nil,
		},
		{
			name:        "empty string",
			input:       "",
			expectedErr: domain.ErrInvalidCurrencyPair,
		},
		{
			name:        "identical currencies",
			input:       "EUR/EUR",
			expectedErr: domain.ErrIdenticalCurrencies,
		},
		{
			name:        "invalid length short",
			input:       "EU/MXN",
			expectedErr: domain.ErrInvalidCurrencyPair,
		},
		{
			name:        "invalid non-alphabetic",
			input:       "EU1/MXN",
			expectedErr: domain.ErrInvalidCurrencyPair,
		},
		{
			name:        "multiple slashes",
			input:       "EUR/USD/MXN",
			expectedErr: domain.ErrInvalidCurrencyPair,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cp, err := domain.ParseCurrencyPair(tt.input)
			if tt.expectedErr != nil {
				require.Error(t, err)
				assert.ErrorIs(t, err, tt.expectedErr)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expectedBase, cp.Base)
				assert.Equal(t, tt.expectedQuote, cp.Quote)
				assert.Equal(t, tt.expectedBase+"/"+tt.expectedQuote, cp.String())
			}
		})
	}
}
