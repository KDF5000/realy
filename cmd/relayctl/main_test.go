package main

import (
	"bytes"
	"context"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/KDF5000/relay"
	"github.com/KDF5000/relay/controlplane"
	"github.com/KDF5000/relay/node"
	"github.com/KDF5000/relay/transport/httpapi"
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

func TestVersionAndDoctor(t *testing.T) {
	service := controlplane.New(time.Minute)
	server := httptest.NewServer(httpapi.NewHandler(service))
	defer server.Close()
	client := httpapi.NewClient(server.URL)
	_, err := client.RegisterNode(context.Background(), controlplane.NodeRegistration{
		ID: "doctor-node", Version: "test", ProtocolVersion: relay.ProtocolVersion, Capacity: 1,
		Runtimes: []controlplane.Runtime{{ID: "doctor-node/mock", Provider: "mock", Version: "1.0.0", State: "healthy", Models: []string{"test-model"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if err := run(context.Background(), []string{"--version"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	if output := stdout.String(); !strings.Contains(output, "relayctl") || !strings.Contains(output, "protocol "+relay.ProtocolVersion) {
		t.Fatalf("version output = %q", output)
	}
	stdout.Reset()
	if err := run(context.Background(), []string{"--server", server.URL, "doctor"}, &stdout, &stderr); err != nil {
		t.Fatal(err)
	}
	output := stdout.String()
	for _, expected := range []string{"[ok] server health", "[ok] host authentication", "[ok] runtime mock", "[ok] model discovery", "[skip] execution probe"} {
		if !strings.Contains(output, expected) {
			t.Fatalf("doctor output missing %q:\n%s", expected, output)
		}
	}
}

func TestDoctorExecutionProbe(t *testing.T) {
	service := controlplane.New(time.Minute)
	server := httptest.NewServer(httpapi.NewHandler(service))
	defer server.Close()
	client := httpapi.NewClient(server.URL)
	artifactPath := t.TempDir() + "/doctor.txt"
	if err := os.WriteFile(artifactPath, []byte("RELAY_DOCTOR_OK"), 0o600); err != nil {
		t.Fatal(err)
	}
	worker := &node.Worker{
		Registration: controlplane.NodeRegistration{
			ID: "doctor-node", Version: "test", ProtocolVersion: relay.ProtocolVersion, Capacity: 1,
			Runtimes: []controlplane.Runtime{{ID: "doctor-node/mock", Provider: "mock", State: "healthy", Models: []string{"test-model"}}},
		},
		ControlPlane: client,
		Executors: node.ExecutorMap{"mock": relay.ExecutorFunc(func(context.Context, relay.Execution) (relay.Result, error) {
			return relay.Result{Summary: "RELAY_DOCTOR_OK", Artifacts: []relay.Artifact{{Type: "message", Name: "doctor.txt", Ref: artifactPath}}}, nil
		})},
	}
	if _, err := worker.Register(context.Background()); err != nil {
		t.Fatal(err)
	}
	workerCtx, cancelWorker := context.WithCancel(context.Background())
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		worker.RunPool(workerCtx, workerCtx, 5*time.Millisecond, func(err error) { t.Errorf("worker: %v", err) })
	}()
	defer func() {
		cancelWorker()
		<-workerDone
	}()

	var stdout, stderr bytes.Buffer
	err := run(context.Background(), []string{"--server", server.URL, "doctor", "--execute", "--provider", "mock", "--timeout", "5s"}, &stdout, &stderr)
	if err != nil {
		t.Fatalf("doctor execution: %v\n%s", err, stdout.String())
	}
	if output := stdout.String(); !strings.Contains(output, "[ok] artifact delivery · 1 artifact(s)") {
		t.Fatalf("doctor output missing artifact result:\n%s", output)
	}
}

func TestRuntimeListAndRunSubmit(t *testing.T) {
	service := controlplane.New(time.Minute)
	server := httptest.NewServer(httpapi.NewHandler(service))
	defer server.Close()
	client := httpapi.NewClient(server.URL)
	_, err := client.RegisterNode(context.Background(), controlplane.NodeRegistration{ProtocolVersion: relay.ProtocolVersion,
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
