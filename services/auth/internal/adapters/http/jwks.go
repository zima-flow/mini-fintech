package authhttp

import (
	"fmt"
	"net/http"
)

const JWKSPath = "/.well-known/jwks.json"

const jwksCacheMaxAge = 300

type JWKSSource interface {
	PublicJWKS() ([]byte, error)
}

func Handler(src JWKSSource) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+JWKSPath, func(w http.ResponseWriter, _ *http.Request) {
		data, err := src.PublicJWKS()
		if err != nil {
			http.Error(w, "jwks unavailable", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", fmt.Sprintf("public, max-age=%d", jwksCacheMaxAge))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
	})
	return mux
}
