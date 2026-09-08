package command_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"testing"

	"github.com/KDF5000/realy"
	runtimecommand "github.com/KDF5000/realy/runtime/command"
)

func TestRuntimeHelper(t *testing.T) {
	if os.Getenv("REALY_RUNTIME_HELPER") != "1" {
		return
	}
	_, _ = io.ReadAll(os.Stdin)
	call := realy.CapabilityCall{Name: "issue.read", Version: "1", Resource: "MUL-42", IdempotencyKey: "runtime-tool-call"}
	body, _ := json.Marshal(call)
	request, _ := http.NewRequest(http.MethodPost, os.Getenv("REALY_TOOL_URL")+"/v1/call", bytes.NewReader(body))
	request.Header.Set("Authorization", "Bearer "+os.Getenv("REALY_TOOL_TOKEN"))
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		os.Exit(2)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		os.Exit(3)
	}
	result := realy.Result{Summary: "runtime used the local Realy tool bridge"}
	_ = json.NewEncoder(os.Stdout).Encode(result)
	os.Exit(0)
}

func TestCommandRuntimeCanUseScopedToolBridge(t *testing.T) {
	providerCalls := 0
	provider := realy.CapabilityProviderFunc(func(_ context.Context, request realy.CapabilityRequest) (json.RawMessage, error) {
		providerCalls++
		return json.Marshal(map[string]string{"id": request.Resource})
	})
	run := realy.Run{ID: "run", AgentID: "agent"}
	invoker := realy.NewCapabilityInvoker(realy.CapabilityInvokerOptions{Run: run, Grants: []realy.CapabilityGrant{{Name: "issue.read", Version: "1", Resources: []string{"MUL-42"}}}, Provider: provider})
	executor := runtimecommand.Executor{Command: os.Args[0], Args: []string{"-test.run=TestRuntimeHelper"}, Env: map[string]string{"REALY_RUNTIME_HELPER": "1"}}
	result, err := executor.Execute(context.Background(), realy.Execution{RunID: "run", AttemptID: "attempt", AgentID: "agent", Capabilities: invoker})
	if err != nil {
		t.Fatal(err)
	}
	if result.Summary != "runtime used the local Realy tool bridge" || providerCalls != 1 {
		t.Fatalf("unexpected result=%#v provider calls=%d", result, providerCalls)
	}
}
