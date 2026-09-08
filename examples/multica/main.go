package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"time"

	"github.com/KDF5000/realy"
	"github.com/KDF5000/realy/binding"
	"github.com/KDF5000/realy/controlplane"
	"github.com/KDF5000/realy/node"
	"github.com/KDF5000/realy/sdk"
	"github.com/KDF5000/realy/transport/httpapi"
)

func main() {
	ctx := context.Background()
	workRoot, err := os.MkdirTemp("", "realy-fleet-demo-")
	if err != nil {
		log.Fatal(err)
	}

	// This is Multica's business API. Realy only sees a capability envelope.
	multicaAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request realy.CapabilityRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"output": map[string]any{"id": request.Resource, "title": "Run agents on a reusable machine fleet", "served_by": "Multica HTTP capability"}})
	}))
	defer multicaAPI.Close()

	service := controlplane.New(30 * time.Second)
	server := httptest.NewServer(httpapi.NewHandler(service))
	defer server.Close()
	transport := httpapi.NewClient(server.URL)
	host := sdk.New(transport)

	registry := binding.NewRegistry()
	if err := registry.Register(binding.Descriptor{Name: "issue.read", Version: "1", Kind: "http"}, binding.HTTPProvider{Endpoint: multicaAPI.URL}); err != nil {
		log.Fatal(err)
	}

	compatible := &node.Worker{
		Registration: controlplane.NodeRegistration{ID: "node-macos-codex", Labels: map[string]string{"os": "darwin", "pool": "engineering"}, Runtimes: []controlplane.Runtime{{Provider: "mock", Version: "1"}}, Capacity: 2},
		ControlPlane: transport, Bindings: registry,
		Executors: node.ExecutorMap{"mock": demoExecutor{workRoot: workRoot}},
	}
	incompatible := &node.Worker{
		Registration: controlplane.NodeRegistration{ID: "node-linux-claude", Labels: map[string]string{"os": "linux", "pool": "engineering"}, Runtimes: []controlplane.Runtime{{Provider: "claude", Version: "1"}}, Capacity: 2},
		ControlPlane: transport, Bindings: binding.NewRegistry(),
		Executors: node.ExecutorMap{"claude": demoExecutor{workRoot: workRoot}},
	}
	if _, err := compatible.Register(ctx); err != nil {
		log.Fatal(err)
	}
	if _, err := incompatible.Register(ctx); err != nil {
		log.Fatal(err)
	}

	queued, err := host.Submit(ctx, demoRequest())
	if err != nil {
		log.Fatal(err)
	}
	if _, err := incompatible.RunOnce(ctx); !errors.Is(err, controlplane.ErrNoAssignment) {
		log.Fatalf("scheduler selected incompatible node: %v", err)
	}
	if _, err := compatible.RunOnce(ctx); err != nil {
		log.Fatal(err)
	}
	completed, err := host.Run(ctx, queued.ID)
	if err != nil {
		log.Fatal(err)
	}
	events, err := host.Events(ctx, queued.ID)
	if err != nil {
		log.Fatal(err)
	}
	nodes, err := host.Nodes(ctx)
	if err != nil {
		log.Fatal(err)
	}

	output := struct {
		Run      realy.Run           `json:"run"`
		Events   []realy.Event       `json:"events"`
		Nodes    []controlplane.Node `json:"nodes"`
		WorkRoot string              `json:"work_root"`
	}{completed, events, nodes, workRoot}
	encoded, _ := json.MarshalIndent(output, "", "  ")
	fmt.Println(string(encoded))
}

type demoExecutor struct{ workRoot string }

func (e demoExecutor) Execute(ctx context.Context, execution realy.Execution) (realy.Result, error) {
	workDir := filepath.Join(e.workRoot, execution.RunID)
	path, err := realy.MaterializeInstructions(workDir, "mock", execution.Instructions)
	if err != nil {
		return realy.Result{}, err
	}
	issue, err := execution.Capabilities.Call(ctx, realy.CapabilityCall{Name: "issue.read", Version: "1", Resource: execution.Source.ExternalID, IdempotencyKey: "read-primary-issue"})
	if err != nil {
		return realy.Result{}, err
	}
	output, _ := json.Marshal(map[string]any{"issue": json.RawMessage(issue.Output), "prompt": execution.Instructions.Prompt})
	return realy.Result{Summary: "Compatible Realy node completed the distributed run", Output: output, Artifacts: []realy.Artifact{{Type: "instruction_file", Ref: path, Name: filepath.Base(path)}}}, nil
}

func demoRequest() realy.Request {
	fragment := func(id, title, content string) realy.InstructionFragment {
		return realy.InstructionFragment{ID: id, Version: "1", Title: title, Content: content}
	}
	return realy.Request{
		AgentID: "engineering-agent", IdempotencyKey: "distributed-demo-1", Runtime: realy.RuntimeRequirement{Provider: "mock", Labels: map[string]string{"pool": "engineering"}},
		Source: realy.Source{Kind: "multica.issue", ExternalID: "MUL-42"}, Input: realy.Input{Type: "task", Version: "1", Prompt: "Read the issue and propose the smallest implementation slice."},
		Instructions: realy.InstructionBundle{Runtime: []realy.InstructionFragment{fragment("runtime", "Runtime contract", "Return a durable result before exiting.")}, Host: []realy.InstructionFragment{fragment("host", "Multica contract", "Use granted capabilities for live business data.")}, Turn: []realy.InstructionFragment{fragment("turn", "Current focus", "Validate multi-machine scheduling.")}},
		Capabilities: []realy.CapabilityGrant{{Name: "issue.read", Version: "1", Effect: "read", Resources: []string{"MUL-42"}}}, Principal: realy.Principal{Type: "user", ID: "demo-user", AccountableID: "demo-user"},
	}
}
