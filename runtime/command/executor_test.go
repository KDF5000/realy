package command_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"testing"

	"github.com/KDF5000/relay"
	runtimecommand "github.com/KDF5000/relay/runtime/command"
)

func TestRuntimeHelper(t *testing.T) {
	if os.Getenv("RELAY_RUNTIME_HELPER") != "1" {
		return
	}
	_, _ = io.ReadAll(os.Stdin)
	call := relay.CapabilityCall{Name: "issue.read", Version: "1", Resource: "MUL-42", IdempotencyKey: "runtime-tool-call"}
	body, _ := json.Marshal(call)
	request, _ := http.NewRequest(http.MethodPost, os.Getenv("RELAY_TOOL_URL")+"/v1/call", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+os.Getenv("RELAY_TOOL_TOKEN"))
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		os.Exit(2)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		os.Exit(3)
	}
	result := relay.Result{Summary: "runtime used the local Relay tool bridge"}
	_ = json.NewEncoder(os.Stdout).Encode(result)
	os.Exit(0)
}

func TestCommandRuntimeCanUseScopedToolBridge(t *testing.T) {
	providerCalls := 0
	provider := relay.CapabilityProviderFunc(func(_ context.Context, request relay.CapabilityRequest) (json.RawMessage, error) {
		providerCalls++
		return json.Marshal(map[string]string{"id": request.Resource})
	})
	run := relay.Run{ID: "run", AgentID: "agent"}
	invoker := relay.NewCapabilityInvoker(relay.CapabilityInvokerOptions{Run: run, Grants: []relay.CapabilityGrant{{Name: "issue.read", Version: "1", Resources: []string{"MUL-42"}}}, Provider: provider})
	executor := runtimecommand.Executor{Command: os.Args[0], Args: []string{"-test.run=TestRuntimeHelper"}, Env: map[string]string{"RELAY_RUNTIME_HELPER": "1"}}
	result, err := executor.Execute(context.Background(), relay.Execution{RunID: "run", AttemptID: "attempt", AgentID: "agent", Capabilities: invoker})
	if err != nil {
		t.Fatal(err)
	}
	if result.Summary != "runtime used the local Relay tool bridge" || providerCalls != 1 {
		t.Fatalf("unexpected result=%#v provider calls=%d", result, providerCalls)
	}
}
