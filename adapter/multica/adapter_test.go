package multica_test

import (
	"context"
	"testing"

	"github.com/KDF5000/relay"
	adapter "github.com/KDF5000/relay/adapter/multica"
)

type hostFunc func(context.Context, relay.Request) (relay.Run, error)

func (f hostFunc) Submit(ctx context.Context, r relay.Request) (relay.Run, error) { return f(ctx, r) }
func TestDispatchIssueKeepsBusinessSemanticsInAdapter(t *testing.T) {
	called := false
	a := adapter.Adapter{Runtime: relay.RuntimeRequirement{Provider: "codex"}, Host: hostFunc(func(_ context.Context, r relay.Request) (relay.Run, error) {
		called = true
		if r.Source.Kind != "multica.issue" || r.Source.ExternalID != "MUL-42" || len(r.Capabilities) != 1 || r.Capabilities[0].Resources[0] != "MUL-42" {
			t.Fatalf("unexpected request: %+v", r)
		}
		return relay.Run{ID: "run-1"}, nil
	})}
	if _, err := a.DispatchIssue(context.Background(), adapter.IssueTask{IssueID: "MUL-42", AgentID: "agent", Prompt: "work", IdempotencyKey: "once"}); err != nil {
		t.Fatal(err)
	}
	if !called {
		t.Fatal("host was not called")
	}
}
