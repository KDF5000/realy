package binding_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"testing"

	"github.com/KDF5000/realy"
	"github.com/KDF5000/realy/binding"
)

func TestExecBindingHelper(t *testing.T) {
	if os.Getenv("REALY_EXEC_HELPER") != "1" {
		return
	}
	var request realy.CapabilityRequest
	if err := json.NewDecoder(os.Stdin).Decode(&request); err != nil {
		os.Exit(2)
	}
	fmt.Printf(`{"output":{"resource":%q,"implementation":"user-cli"}}`, request.Resource)
	os.Exit(0)
}

func TestExecProviderUsesStructuredStdinAndStdout(t *testing.T) {
	provider := binding.ExecProvider{Config: binding.Exec{Command: os.Args[0], Args: []string{"-test.run=TestExecBindingHelper"}, Env: map[string]string{"REALY_EXEC_HELPER": "1"}}}
	output, err := provider.Invoke(context.Background(), realy.CapabilityRequest{Name: "issue.read", Version: "1", Resource: "MUL-42"})
	if err != nil {
		t.Fatal(err)
	}
	var value map[string]string
	if err := json.Unmarshal(output, &value); err != nil {
		t.Fatal(err)
	}
	if value["resource"] != "MUL-42" || value["implementation"] != "user-cli" {
		t.Fatalf("unexpected CLI output: %s", output)
	}
}
