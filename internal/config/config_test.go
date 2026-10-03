package config_test

import (
	"os"
	"testing"
	"time"

	"github.com/ArtemPak289/Currency-exchange-rate-service/internal/config"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConfigLoad(t *testing.T) {
	t.Run("default configuration values", func(t *testing.T) {
		cfg, err := config.Load()
		require.NoError(t, err)

		assert.NotEmpty(t, cfg.HTTPPort)
		assert.Equal(t, ":"+cfg.HTTPPort, cfg.ServerAddr())
		assert.NotEmpty(t, cfg.APIKey)
		assert.NotEmpty(t, cfg.APIBaseURL)
		assert.Greater(t, cfg.WorkerCount, 0)
	})

	t.Run("custom environment variable overrides", func(t *testing.T) {
		os.Setenv("HTTP_PORT", "9999")
		os.Setenv("WORKER_COUNT", "8")
		os.Setenv("USE_MOCK_FETCHER", "true")
		os.Setenv("HTTP_READ_TIMEOUT", "20s")
		defer func() {
			os.Unsetenv("HTTP_PORT")
			os.Unsetenv("WORKER_COUNT")
			os.Unsetenv("USE_MOCK_FETCHER")
			os.Unsetenv("HTTP_READ_TIMEOUT")
		}()

		cfg, err := config.Load()
		require.NoError(t, err)

		assert.Equal(t, "9999", cfg.HTTPPort)
		assert.Equal(t, ":9999", cfg.ServerAddr())
		assert.Equal(t, 8, cfg.WorkerCount)
		assert.True(t, cfg.UseMockFetcher)
		assert.Equal(t, 20*time.Second, cfg.ReadTimeout)
	})
}
