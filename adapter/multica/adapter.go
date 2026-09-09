// Package multica contains the thin business adapter between Multica and Relay.
// All issue semantics stay here; the Relay scheduler remains business-agnostic.
package multica

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/KDF5000/relay"
	"github.com/KDF5000/relay/binding"
	"github.com/KDF5000/relay/sdk"
)

type Adapter struct {
	Host    sdk.Submitter
	Runtime relay.RuntimeRequirement
}
type IssueTask struct {
	IssueID        string
	AgentID        string
	Prompt         string
	SessionID      string
	IdempotencyKey string
	Principal      relay.Principal
	Context        json.RawMessage
}

func (a Adapter) DispatchIssue(ctx context.Context, task IssueTask) (relay.Run, error) {
	if a.Host == nil {
		return relay.Run{}, errors.New("multica adapter: Relay host is required")
	}
	if task.IssueID == "" || task.AgentID == "" || task.Prompt == "" || task.IdempotencyKey == "" {
		return relay.Run{}, errors.New("multica adapter: issue, agent, prompt, and idempotency key are required")
	}
	return a.Host.Submit(ctx, relay.Request{SessionID: task.SessionID, AgentID: task.AgentID, IdempotencyKey: task.IdempotencyKey, Runtime: a.Runtime, Source: relay.Source{Kind: "multica.issue", ExternalID: task.IssueID}, Input: relay.Input{Type: "task", Version: "1", Prompt: task.Prompt, Data: task.Context}, Principal: task.Principal, Capabilities: []relay.CapabilityGrant{{Name: "issue.read", Version: "1", Effect: "read", Resources: []string{task.IssueID}}}})
}

// CapabilityBinding delegates business operations to the installed Multica CLI.
// Relay never receives Multica credentials; the CLI owns its normal auth profile.
func CapabilityBinding(command string, args ...string) (binding.Descriptor, relay.CapabilityProvider) {
	if command == "" {
		command = "multica"
	}
	base := []string{"capability", "invoke", "--protocol", "relay-v1"}
	base = append(base, args...)
	return binding.Descriptor{Name: "issue.read", Version: "1", Kind: "exec"}, binding.ExecProvider{Config: binding.Exec{Command: command, Args: base, InheritEnv: true}}
}
