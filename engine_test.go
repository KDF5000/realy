package realy_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/KDF5000/realy"
)

type hostCapabilities struct {
	calls     int
	principal realy.Principal
}

func TestConcurrentCapabilityCallsAreCollapsed(t *testing.T) {
	var calls atomic.Int32
	provider := realy.CapabilityProviderFunc(func(_ context.Context, _ realy.CapabilityRequest) (json.RawMessage, error) {
		calls.Add(1)
		return json.RawMessage(`{"ok":true}`), nil
	})
	executor := realy.ExecutorFunc(func(ctx context.Context, execution realy.Execution) (realy.Result, error) {
		const workers = 8
		results := make(chan realy.CapabilityResult, workers)
		errorsFound := make(chan error, workers)
		var group sync.WaitGroup
		for range workers {
			group.Add(1)
			go func() {
				defer group.Done()
				result, err := execution.Capabilities.Call(ctx, realy.CapabilityCall{Name: "issue.read", Version: "1", Resource: "I-1", IdempotencyKey: "shared"})
				results <- result
				errorsFound <- err
			}()
		}
		group.Wait()
		close(results)
		close(errorsFound)
		for err := range errorsFound {
			if err != nil {
				return realy.Result{}, err
			}
		}
		firstID := ""
		for result := range results {
			if firstID == "" {
				firstID = result.CallID
			}
			if result.CallID != firstID {
				return realy.Result{}, errors.New("concurrent calls returned different IDs")
			}
		}
		return realy.Result{Summary: "done"}, nil
	})
	engine, _ := realy.New(realy.Options{Executor: executor, Capabilities: provider})
	_, err := engine.Execute(context.Background(), realy.Request{
		AgentID: "agent", IdempotencyKey: "concurrent", Input: realy.Input{Prompt: "work"},
		Capabilities: []realy.CapabilityGrant{{Name: "issue.read", Version: "1", Resources: []string{"I-1"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("expected one host call, got %d", calls.Load())
	}
}

func (h *hostCapabilities) Invoke(_ context.Context, request realy.CapabilityRequest) (json.RawMessage, error) {
	h.calls++
	h.principal = request.Principal
	return json.Marshal(map[string]string{"resource": request.Resource})
}

func TestEmbeddedExecution(t *testing.T) {
	host := &hostCapabilities{}
	executor := realy.ExecutorFunc(func(ctx context.Context, execution realy.Execution) (realy.Result, error) {
		call := realy.CapabilityCall{Name: "issue.read", Version: "1", Resource: "I-1", IdempotencyKey: "once"}
		first, err := execution.Capabilities.Call(ctx, call)
		if err != nil {
			return realy.Result{}, err
		}
		second, err := execution.Capabilities.Call(ctx, call)
		if err != nil {
			return realy.Result{}, err
		}
		if first.CallID != second.CallID {
			return realy.Result{}, errors.New("capability call was not idempotent")
		}
		return realy.Result{Summary: "done"}, nil
	})
	engine, err := realy.New(realy.Options{Executor: executor, Capabilities: host})
	if err != nil {
		t.Fatal(err)
	}
	request := realy.Request{
		AgentID: "agent", IdempotencyKey: "request-1", Input: realy.Input{Prompt: "work"},
		Capabilities: []realy.CapabilityGrant{{Name: "issue.read", Version: "1", Resources: []string{"I-1"}}},
		Principal:    realy.Principal{Type: "user", ID: "U-1"},
	}
	run, err := engine.Execute(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != realy.RunSucceeded || run.Result == nil || run.Result.Summary != "done" {
		t.Fatalf("unexpected run: %#v", run)
	}
	if host.calls != 1 || host.principal.ID != "U-1" {
		t.Fatalf("unexpected host invocation: calls=%d principal=%#v", host.calls, host.principal)
	}
	again, err := engine.Execute(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if again.ID != run.ID || host.calls != 1 {
		t.Fatalf("request idempotency failed: %#v calls=%d", again, host.calls)
	}
	events, err := engine.Events(context.Background(), run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 7 {
		t.Fatalf("expected 7 events, got %d", len(events))
	}
}

func TestCapabilityScopeIsEnforcedBeforeHostCode(t *testing.T) {
	host := &hostCapabilities{}
	executor := realy.ExecutorFunc(func(ctx context.Context, execution realy.Execution) (realy.Result, error) {
		_, err := execution.Capabilities.Call(ctx, realy.CapabilityCall{Name: "issue.read", Resource: "I-2", IdempotencyKey: "outside"})
		return realy.Result{}, err
	})
	engine, _ := realy.New(realy.Options{Executor: executor, Capabilities: host})
	run, err := engine.Execute(context.Background(), realy.Request{
		AgentID: "agent", IdempotencyKey: "request-2", Input: realy.Input{Prompt: "work"},
		Capabilities: []realy.CapabilityGrant{{Name: "issue.read", Version: "1", Resources: []string{"I-1"}}},
	})
	if !errors.Is(err, realy.ErrResourceOutOfScope) || run.Status != realy.RunFailed || host.calls != 0 {
		t.Fatalf("scope enforcement failed: run=%#v err=%v host calls=%d", run, err, host.calls)
	}
}
