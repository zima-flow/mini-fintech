package config_test

import (
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/zima-flow/go-mentor/mini-fintech/platform/config"
)

type testConfig struct {
	Host     string        `field:"TEST_HOST"`
	Port     int           `field:"TEST_PORT"`
	Debug    bool          `field:"TEST_DEBUG,optional"`
	Timeout  time.Duration `field:"TEST_TIMEOUT"`
	Optional string        `field:"TEST_OPTIONAL,optional"`
}

func env(values map[string]string) func(string) string {
	return func(key string) string { return values[key] }
}

func TestLoad_DecodesSupportedTypes(t *testing.T) {
	t.Parallel()
	cfg, err := config.Load[testConfig](env(map[string]string{
		"TEST_HOST":    "localhost",
		"TEST_PORT":    "8080",
		"TEST_DEBUG":   "true",
		"TEST_TIMEOUT": "5s",
	}))
	require.NoError(t, err)
	require.Equal(t, "localhost", cfg.Host)
	require.Equal(t, 8080, cfg.Port)
	require.True(t, cfg.Debug)
	require.Equal(t, 5*time.Second, cfg.Timeout)
	require.Empty(t, cfg.Optional)
}

func TestLoad_MissingRequired_NamesKey(t *testing.T) {
	t.Parallel()
	_, err := config.Load[testConfig](env(map[string]string{
		"TEST_HOST":    "localhost",
		"TEST_TIMEOUT": "5s",
	}))
	require.Error(t, err)
	require.Contains(t, err.Error(), "TEST_PORT")
}

func TestLoad_InvalidInt_NamesKey(t *testing.T) {
	t.Parallel()
	_, err := config.Load[testConfig](env(map[string]string{
		"TEST_HOST":    "localhost",
		"TEST_PORT":    "not-a-number",
		"TEST_TIMEOUT": "5s",
	}))
	require.Error(t, err)
	require.Contains(t, err.Error(), "TEST_PORT")
}

func TestLoad_InvalidDuration_NamesKey(t *testing.T) {
	t.Parallel()
	_, err := config.Load[testConfig](env(map[string]string{
		"TEST_HOST":    "localhost",
		"TEST_PORT":    "8080",
		"TEST_TIMEOUT": "fast",
	}))
	require.Error(t, err)
	require.Contains(t, err.Error(), "TEST_TIMEOUT")
}

type validatingConfig struct {
	Port int `field:"TEST_VPORT"`
}

func (c validatingConfig) Validate() error {
	if c.Port <= 0 {
		return errors.New("port must be positive")
	}
	return nil
}

func TestLoad_CallsValidator(t *testing.T) {
	t.Parallel()
	_, err := config.Load[validatingConfig](env(map[string]string{"TEST_VPORT": "0"}))
	require.ErrorContains(t, err, "port must be positive")

	cfg, err := config.Load[validatingConfig](env(map[string]string{"TEST_VPORT": "1"}))
	require.NoError(t, err)
	require.Equal(t, 1, cfg.Port)
}
