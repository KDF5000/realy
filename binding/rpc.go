package binding

import (
	"context"
	"encoding/json"

	"github.com/KDF5000/relay"
)

// RPCClient lets generated gRPC, Connect, or another typed RPC client become a binding
// without making Relay core depend on one RPC implementation.
type RPCClient interface {
	InvokeCapability(context.Context, relay.CapabilityRequest) (json.RawMessage, error)
}

type RPCProvider struct{ Client RPCClient }

func (p RPCProvider) Invoke(ctx context.Context, request relay.CapabilityRequest) (json.RawMessage, error) {
	return p.Client.InvokeCapability(ctx, request)
}
