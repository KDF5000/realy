package main

import (
	"bytes"
	"context"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/KDF5000/realy/controlplane"
	"github.com/KDF5000/realy/transport/httpapi"
)

func TestParseGrant(t *testing.T) {
	grant, err := parseGrant("issue.read@1:read:MUL-42,MUL-43")
	if err != nil {
		t.Fatal(err)
	}
	if grant.Name != "issue.read" || grant.Version != "1" || grant.Effect != "read" || len(grant.Resources) != 2 {
		t.Fatalf("unexpected grant: %+v", grant)
	}
}

func TestRuntimeListAndRunSubmit(t *testing.T) {
	service := controlplane.New(time.Minute)
	server := httptest.NewServer(httpapi.NewHandler(service))
	defer server.Close()
	client := httpapi.NewClient(server.URL)
	_, err := client.RegisterNode(context.Background(), controlplane.NodeRegistration{
		ID: "mac-1", Capacity: 2,
		Runtimes:     []controlplane.Runtime{{Provider: "codex", Version: "codex-cli test"}},
		Capabilities: []controlplane.Capability{{Name: "issue.read", Version: "1", Kind: "exec"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if err := run(context.Background(), []string{"--server", server.URL, "runtime", "list"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if output := stdout.String(); !strings.Contains(output, "mac-1") || !strings.Contains(output, "issue.read@1") {
		t.Fatalf("unexpected runtime output:\n%s", output)
	}
	stdout.Reset()
	if err := run(context.Background(), []string{"--server", server.URL, "run", "submit", "--prompt", "Read the issue", "--grant", "issue.read@1:read:MUL-42", "--watch=false"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if output := stdout.String(); !strings.Contains(output, "Submitted run_") || !strings.Contains(output, "status=queued") {
		t.Fatalf("unexpected submit output:\n%s", output)
	}
	runID := strings.Fields(stdout.String())[1]
	stdout.Reset()
	if err := run(context.Background(), []string{"--server", server.URL, "run", "cancel", runID, "--reason", "test"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if output := stdout.String(); !strings.Contains(output, "STATUS") || !strings.Contains(output, "cancelled") {
		t.Fatalf("unexpected cancel output:\n%s", output)
	}
}
