package sdk_test

import (
	"context"
	"fmt"

	"github.com/KDF5000/relay"
	"github.com/KDF5000/relay/controlplane"
	"github.com/KDF5000/relay/sdk"
	"github.com/KDF5000/relay/transport/httpapi"
)

var (
	_ sdk.Backend = (*httpapi.Client)(nil)
	_ sdk.Backend = (*sdk.Client)(nil)
)

// A dispatch-only application needs no node, artifact or interaction methods.
func ExampleSubmitter() {
	var host sdk.Submitter = controlplane.New(0)
	run, err := host.Submit(context.Background(), relay.Request{
		AgentID: "external-agent", IdempotencyKey: "example-task",
		Runtime: relay.RuntimeRequirement{Provider: "codex"},
		Input:   relay.Input{Prompt: "Inspect the repository"},
	})
	if err != nil {
		panic(err)
	}
	fmt.Println(run.Status)
	// Output: queued
}
