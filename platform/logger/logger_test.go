package logger

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewLogger_JSONHasServiceAndHonorsLevel(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer

	l := newLogger(&buf, "info", "example")
	l.Info("hello", "k", "v")

	var rec map[string]any
	require.NoError(t, json.Unmarshal(buf.Bytes(), &rec))
	require.Equal(t, "example", rec["service"])
	require.Equal(t, "INFO", rec["level"])
	require.Equal(t, "hello", rec["msg"])
}

func TestNewLogger_DebugUsesTextHandler(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer

	l := newLogger(&buf, "debug", "example")
	l.Debug("hi")

	require.Contains(t, buf.String(), "service=example")
	require.NotContains(t, buf.String(), `"level"`)
}

func TestNew_ReturnsConfiguredLogger(t *testing.T) {
	t.Parallel()
	require.NotNil(t, New("info", "example"))
}
