package sdk_test

import (
	"context"
	"fmt"

	"github.com/KDF5000/realy"
	"github.com/KDF5000/realy/controlplane"
	"github.com/KDF5000/realy/sdk"
	"github.com/KDF5000/realy/transport/httpapi"
)

var (
	_ sdk.Backend = (*httpapi.Client)(nil)
	_ sdk.Backend = (*sdk.Client)(nil)
)

// A dispatch-only application needs no node, artifact or interaction methods.
func ExampleSubmitter() {
	var host sdk.Submitter = controlplane.New(0)
	run, err := host.Submit(context.Background(), realy.Request{
		AgentID: "external-agent", IdempotencyKey: "example-task",
		Runtime: realy.RuntimeRequirement{Provider: "codex"},
		Input:   realy.Input{Prompt: "Inspect the repository"},
	})
	if err != nil {
		panic(err)
	}
	fmt.Println(run.Status)
	// Output: queued
}
