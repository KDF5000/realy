package binding

import (
	"context"
	"encoding/json"

	"github.com/KDF5000/realy"
)

// RPCClient lets generated gRPC, Connect, or another typed RPC client become a binding
// without making Realy core depend on one RPC implementation.
type RPCClient interface {
	InvokeCapability(context.Context, realy.CapabilityRequest) (json.RawMessage, error)
}

type RPCProvider struct{ Client RPCClient }

func (p RPCProvider) Invoke(ctx context.Context, request realy.CapabilityRequest) (json.RawMessage, error) {
	return p.Client.InvokeCapability(ctx, request)
}
