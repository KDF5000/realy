package toolbridge_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/KDF5000/realy"
	"github.com/KDF5000/realy/runtime/toolbridge"
)

func TestFileBridgeCallsScopedInvoker(t *testing.T) {
	provider := realy.CapabilityProviderFunc(func(_ context.Context, request realy.CapabilityRequest) (json.RawMessage, error) {
		return json.Marshal(map[string]string{"resource": request.Resource})
	})
	invoker := realy.NewCapabilityInvoker(realy.CapabilityInvokerOptions{
		Run:      realy.Run{ID: "run-1", AgentID: "agent-1"},
		Grants:   []realy.CapabilityGrant{{Name: "issue.read", Version: "1", Effect: "read", Resources: []string{"MUL-42"}}},
		Provider: provider,
	})
	bridge, err := toolbridge.StartFile(invoker, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.Close()
	result, err := toolbridge.CallFile(context.Background(), bridge.Dir, bridge.Token, realy.CapabilityCall{Name: "issue.read", Version: "1", Resource: "MUL-42", IdempotencyKey: "read-1"})
	if err != nil {
		t.Fatal(err)
	}
	if string(result.Output) != `{"resource":"MUL-42"}` {
		t.Fatalf("unexpected output: %s", result.Output)
	}
}

func TestFileBridgeRejectsWrongToken(t *testing.T) {
	invoker := realy.NewCapabilityInvoker(realy.CapabilityInvokerOptions{Provider: realy.CapabilityProviderFunc(func(context.Context, realy.CapabilityRequest) (json.RawMessage, error) { return nil, nil })})
	bridge, err := toolbridge.StartFile(invoker, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer bridge.Close()
	_, err = toolbridge.CallFile(context.Background(), bridge.Dir, "wrong", realy.CapabilityCall{Name: "issue.read", IdempotencyKey: "read-1"})
	if err == nil || err.Error() != "realy tool bridge: unauthorized" {
		t.Fatalf("expected unauthorized error, got %v", err)
	}
}
