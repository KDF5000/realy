// Package multica contains the thin business adapter between Multica and Realy.
// All issue semantics stay here; the Realy scheduler remains business-agnostic.
package multica

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/KDF5000/realy"
	"github.com/KDF5000/realy/binding"
	"github.com/KDF5000/realy/sdk"
)

type Adapter struct {
	Host    sdk.Submitter
	Runtime realy.RuntimeRequirement
}
type IssueTask struct {
	IssueID        string
	AgentID        string
	Prompt         string
	SessionID      string
	IdempotencyKey string
	Principal      realy.Principal
	Context        json.RawMessage
}

func (a Adapter) DispatchIssue(ctx context.Context, task IssueTask) (realy.Run, error) {
	if a.Host == nil {
		return realy.Run{}, errors.New("multica adapter: Realy host is required")
	}
	if task.IssueID == "" || task.AgentID == "" || task.Prompt == "" || task.IdempotencyKey == "" {
		return realy.Run{}, errors.New("multica adapter: issue, agent, prompt, and idempotency key are required")
	}
	return a.Host.Submit(ctx, realy.Request{SessionID: task.SessionID, AgentID: task.AgentID, IdempotencyKey: task.IdempotencyKey, Runtime: a.Runtime, Source: realy.Source{Kind: "multica.issue", ExternalID: task.IssueID}, Input: realy.Input{Type: "task", Version: "1", Prompt: task.Prompt, Data: task.Context}, Principal: task.Principal, Capabilities: []realy.CapabilityGrant{{Name: "issue.read", Version: "1", Effect: "read", Resources: []string{task.IssueID}}}})
}

// CapabilityBinding delegates business operations to the installed Multica CLI.
// Realy never receives Multica credentials; the CLI owns its normal auth profile.
func CapabilityBinding(command string, args ...string) (binding.Descriptor, realy.CapabilityProvider) {
	if command == "" {
		command = "multica"
	}
	base := []string{"capability", "invoke", "--protocol", "realy-v1"}
	base = append(base, args...)
	return binding.Descriptor{Name: "issue.read", Version: "1", Kind: "exec"}, binding.ExecProvider{Config: binding.Exec{Command: command, Args: base, InheritEnv: true}}
}
