package toolbridge

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net"
	"net/http"
	"time"

	"github.com/KDF5000/relay"
)

type Bridge struct {
	server   *http.Server
	listener net.Listener
	Token    string
}

func Start(invoker relay.CapabilityInvoker) (*Bridge, error) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	var secret [24]byte
	if _, err := rand.Read(secret[:]); err != nil {
		listener.Close()
		return nil, err
	}
	token := hex.EncodeToString(secret[:])
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/call", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		var call relay.CapabilityCall
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20)).Decode(&call); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		result, err := invoker.Call(r.Context(), call)
		if err != nil {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(result)
	})
	bridge := &Bridge{server: &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}, listener: listener, Token: token}
	go func() { _ = bridge.server.Serve(listener) }()
	return bridge, nil
}

func (b *Bridge) Close() {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_ = b.server.Shutdown(ctx)
}
func (b *Bridge) URL() string { return "http://" + b.listener.Addr().String() }
