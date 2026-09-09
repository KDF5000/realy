package toolbridge_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/KDF5000/relay"
	"github.com/KDF5000/relay/runtime/toolbridge"
)

func TestFileBridgeCallsScopedInvoker(t *testing.T) {
	provider := relay.CapabilityProviderFunc(func(_ context.Context, request relay.CapabilityRequest) (json.RawMessage, error) {
		return json.Marshal(map[string]string{"resource": request.Resource})
	})
	invoker := relay.NewCapabilityInvoker(relay.CapabilityInvokerOptions{
		Run:      relay.Run{ID: "run-1", AgentID: "agent-1"},
		Grants:   []relay.CapabilityGrant{{Name: "issue.read", Version: "1", Effect: "read", Resources: []string{"MUL-42"}}},
		Provider: provider,
	})
	bridge, err := toolbridge.StartFile(invoker, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.Close()
	result, err := toolbridge.CallFile(context.Background(), bridge.Dir, bridge.Token, relay.CapabilityCall{Name: "issue.read", Version: "1", Resource: "MUL-42", IdempotencyKey: "read-1"})
	if err != nil {
		t.Fatal(err)
	}
	if string(result.Output) != `{"resource":"MUL-42"}` {
		t.Fatalf("unexpected output: %s", result.Output)
	}
}

func TestFileBridgeRejectsWrongToken(t *testing.T) {
	invoker := relay.NewCapabilityInvoker(relay.CapabilityInvokerOptions{Provider: relay.CapabilityProviderFunc(func(context.Context, relay.CapabilityRequest) (json.RawMessage, error) { return nil, nil })})
	bridge, err := toolbridge.StartFile(invoker, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.Close()
	_, err = toolbridge.CallFile(context.Background(), bridge.Dir, "wrong", relay.CapabilityCall{Name: "issue.read", IdempotencyKey: "read-1"})
	if err == nil || err.Error() != "relay tool bridge: unauthorized" {
		t.Fatalf("expected unauthorized error, got %v", err)
	}
}
