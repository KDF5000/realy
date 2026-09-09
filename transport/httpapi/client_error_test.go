package httpapi_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/KDF5000/realy/controlplane"
	"github.com/KDF5000/realy/transport/httpapi"
)

func TestAuthenticationFailureIsNotAnExpiredLease(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"invalid bearer token"}`))
	}))
	defer server.Close()
	err := httpapi.NewClient(server.URL).AppendEvent(context.Background(), "run", "attempt", "lease", "delta", nil, "1")
	if err == nil || errors.Is(err, controlplane.ErrInvalidLease) {
		t.Fatalf("authentication error must preserve pending events: %v", err)
	}
}
