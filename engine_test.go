package relay_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/KDF5000/relay"
)

type hostCapabilities struct {
	calls     int
	principal relay.Principal
}

func TestConcurrentCapabilityCallsAreCollapsed(t *testing.T) {
	var calls atomic.Int32
	provider := relay.CapabilityProviderFunc(func(_ context.Context, _ relay.CapabilityRequest) (json.RawMessage, error) {
		calls.Add(1)
		return json.RawMessage(`{"ok":true}`), nil
	})
	executor := relay.ExecutorFunc(func(ctx context.Context, execution relay.Execution) (relay.Result, error) {
		const workers = 8
		results := make(chan relay.CapabilityResult, workers)
		errorsFound := make(chan error, workers)
		var group sync.WaitGroup
		for range workers {
			group.Add(1)
			go func() {
				defer group.Done()
				result, err := execution.Capabilities.Call(ctx, relay.CapabilityCall{Name: "issue.read", Version: "1", Resource: "I-1", IdempotencyKey: "shared"})
				results <- result
				errorsFound <- err
			}()
		}
		group.Wait()
		close(results)
		close(errorsFound)
		for err := range errorsFound {
			if err != nil {
				return relay.Result{}, err
			}
		}
		firstID := ""
		for result := range results {
			if firstID == "" {
				firstID = result.CallID
			}
			if result.CallID != firstID {
				return relay.Result{}, errors.New("concurrent calls returned different IDs")
			}
		}
		return relay.Result{Summary: "done"}, nil
	})
	engine, _ := relay.New(relay.Options{Executor: executor, Capabilities: provider})
	_, err := engine.Execute(context.Background(), relay.Request{
		AgentID: "agent", IdempotencyKey: "concurrent", Input: relay.Input{Prompt: "work"},
		Capabilities: []relay.CapabilityGrant{{Name: "issue.read", Version: "1", Resources: []string{"I-1"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("expected one host call, got %d", calls.Load())
	}
}

func (h *hostCapabilities) Invoke(_ context.Context, request relay.CapabilityRequest) (json.RawMessage, error) {
	h.calls++
	h.principal = request.Principal
	return json.Marshal(map[string]string{"resource": request.Resource})
}

func TestEmbeddedExecution(t *testing.T) {
	host := &hostCapabilities{}
	executor := relay.ExecutorFunc(func(ctx context.Context, execution relay.Execution) (relay.Result, error) {
		call := relay.CapabilityCall{Name: "issue.read", Version: "1", Resource: "I-1", IdempotencyKey: "once"}
		first, err := execution.Capabilities.Call(ctx, call)
		if err != nil {
			return relay.Result{}, err
		}
		second, err := execution.Capabilities.Call(ctx, call)
		if err != nil {
			return relay.Result{}, err
		}
		if first.CallID != second.CallID {
			return relay.Result{}, errors.New("capability call was not idempotent")
		}
		return relay.Result{Summary: "done"}, nil
	})
	engine, err := relay.New(relay.Options{Executor: executor, Capabilities: host})
	if err != nil {
		t.Fatal(err)
	}
	request := relay.Request{
		AgentID: "agent", IdempotencyKey: "request-1", Input: relay.Input{Prompt: "work"},
		Capabilities: []relay.CapabilityGrant{{Name: "issue.read", Version: "1", Resources: []string{"I-1"}}},
		Principal:    relay.Principal{Type: "user", ID: "U-1"},
	}
	run, err := engine.Execute(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	if run.Status != relay.RunSucceeded || run.Result == nil || run.Result.Summary != "done" {
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
	executor := relay.ExecutorFunc(func(ctx context.Context, execution relay.Execution) (relay.Result, error) {
		_, err := execution.Capabilities.Call(ctx, relay.CapabilityCall{Name: "issue.read", Resource: "I-2", IdempotencyKey: "outside"})
		return relay.Result{}, err
	})
	engine, _ := relay.New(relay.Options{Executor: executor, Capabilities: host})
	run, err := engine.Execute(context.Background(), relay.Request{
		AgentID: "agent", IdempotencyKey: "request-2", Input: relay.Input{Prompt: "work"},
		Capabilities: []relay.CapabilityGrant{{Name: "issue.read", Version: "1", Resources: []string{"I-1"}}},
	})
	if !errors.Is(err, relay.ErrResourceOutOfScope) || run.Status != relay.RunFailed || host.calls != 0 {
		t.Fatalf("scope enforcement failed: run=%#v err=%v host calls=%d", run, err, host.calls)
	}
}
