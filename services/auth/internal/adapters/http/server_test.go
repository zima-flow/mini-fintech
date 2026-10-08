package authhttp_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	authhttp "github.com/zima-flow/go-mentor/mini-fintech/services/auth/internal/adapters/http"
)

func TestServer_Run_ServesAndDrains(t *testing.T) {
	t.Parallel()

	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	srv := authhttp.NewServer(authhttp.ServerConfig{
		Addr:            "127.0.0.1:0",
		Handler:         mux,
		ShutdownTimeout: 2 * time.Second,
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- srv.Run(ctx) }()

	require.Eventually(t, func() bool { return srv.Addr() != nil }, 2*time.Second, 10*time.Millisecond)

	resp, err := http.Get("http://" + srv.Addr().String() + "/health")
	require.NoError(t, err)
	_ = resp.Body.Close()
	require.Equal(t, http.StatusOK, resp.StatusCode)

	cancel()
	select {
	case err := <-done:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("Run did not drain within the bound")
	}
}
