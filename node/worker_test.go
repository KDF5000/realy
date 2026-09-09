package node_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/KDF5000/realy"
	"github.com/KDF5000/realy/controlplane"
	"github.com/KDF5000/realy/node"
)

type failingEvents struct {
	node.ControlPlane
	cause error
}

type lostEventResponse struct {
	node.ControlPlane
	calls int
}

type lostCompletionResponse struct {
	node.ControlPlane
	calls atomic.Int32
}

func (c *lostCompletionResponse) Complete(ctx context.Context, assignment controlplane.Assignment, result realy.Result) error {
	c.calls.Add(1)
	if err := c.ControlPlane.Complete(ctx, assignment, result); err != nil {
		return err
	}
	if c.calls.Load() == 1 {
		return errors.New("completion response lost after commit")
	}
	return nil
}

type slowArtifactControlPlane struct {
	node.ControlPlane
	renewals atomic.Int32
	delay    time.Duration
}

func (c *slowArtifactControlPlane) Renew(ctx context.Context, assignment controlplane.Assignment) (controlplane.LeaseUpdate, error) {
	c.renewals.Add(1)
	return c.ControlPlane.Renew(ctx, assignment)
}

func (c *slowArtifactControlPlane) UploadArtifact(ctx context.Context, assignment controlplane.Assignment, artifact realy.Artifact, content io.Reader) (realy.Artifact, error) {
	select {
	case <-ctx.Done():
		return realy.Artifact{}, ctx.Err()
	case <-time.After(c.delay):
	}
	_, err := io.Copy(io.Discard, content)
	return artifact, err
}

func TestWorkerRetriesLostCompletionResponse(t *testing.T) {
	ctx := context.Background()
	service := controlplane.New(time.Second)
	cp := &lostCompletionResponse{ControlPlane: service}
	worker := &node.Worker{
		Registration: controlplane.NodeRegistration{ID: "completion-node", Runtimes: []controlplane.Runtime{{Provider: "test"}}, Capacity: 1},
		ControlPlane: cp,
		Executors: node.ExecutorMap{"test": realy.ExecutorFunc(func(context.Context, realy.Execution) (realy.Result, error) {
			return realy.Result{Summary: "done"}, nil
		})},
	}
	if _, err := worker.Register(ctx); err != nil {
		t.Fatal(err)
	}
	run, err := service.Submit(ctx, realy.Request{AgentID: "agent", IdempotencyKey: "completion-response", Runtime: realy.RuntimeRequirement{Provider: "test"}, Input: realy.Input{Prompt: "work"}})
	if err != nil {
		t.Fatal(err)
	}
	if completed, err := worker.RunOnce(ctx); err != nil || completed.Status != realy.RunSucceeded {
		t.Fatalf("completed=%+v err=%v", completed, err)
	}
	if cp.calls.Load() != 2 {
		t.Fatalf("completion calls=%d", cp.calls.Load())
	}
	events, _ := service.Events(ctx, run.ID)
	terminal := 0
	for _, event := range events {
		if event.Type == "run.succeeded" {
			terminal++
		}
	}
	if terminal != 1 {
		t.Fatalf("terminal events=%d", terminal)
	}
}

func TestWorkerRenewsLeaseThroughArtifactUpload(t *testing.T) {
	ctx := context.Background()
	service := controlplane.New(30 * time.Millisecond)
	cp := &slowArtifactControlPlane{ControlPlane: service, delay: 120 * time.Millisecond}
	artifactPath := t.TempDir() + "/result.txt"
	if err := os.WriteFile(artifactPath, []byte("result"), 0o600); err != nil {
		t.Fatal(err)
	}
	worker := &node.Worker{
		Registration: controlplane.NodeRegistration{ID: "artifact-node", Runtimes: []controlplane.Runtime{{Provider: "test"}}, Capacity: 1},
		ControlPlane: cp,
		Executors: node.ExecutorMap{"test": realy.ExecutorFunc(func(context.Context, realy.Execution) (realy.Result, error) {
			return realy.Result{Summary: "done", Artifacts: []realy.Artifact{{Name: "result.txt", Ref: artifactPath}}}, nil
		})},
	}
	if _, err := worker.Register(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Submit(ctx, realy.Request{AgentID: "agent", IdempotencyKey: "slow-artifact", Runtime: realy.RuntimeRequirement{Provider: "test"}, Input: realy.Input{Prompt: "work"}}); err != nil {
		t.Fatal(err)
	}
	if completed, err := worker.RunOnce(ctx); err != nil || completed.Status != realy.RunSucceeded {
		t.Fatalf("completed=%+v err=%v", completed, err)
	}
	if cp.renewals.Load() < 2 {
		t.Fatalf("lease renewed only %d times", cp.renewals.Load())
	}
}

func (f *lostEventResponse) AppendEvent(ctx context.Context, run, attempt, lease, kind string, data any, ids ...string) error {
	f.calls++
	if err := f.ControlPlane.AppendEvent(ctx, run, attempt, lease, kind, data, ids...); err != nil {
		return err
	}
	if f.calls == 1 {
		return errors.New("response lost after commit")
	}
	return nil
}

func TestWorkerRetriesLostResponseWithoutDuplicatingOutput(t *testing.T) {
	ctx := context.Background()
	service := controlplane.New(time.Second)
	cp := &lostEventResponse{ControlPlane: service}
	worker := &node.Worker{
		Registration: controlplane.NodeRegistration{ID: "retry-node", Runtimes: []controlplane.Runtime{{Provider: "test"}}, Capacity: 1},
		ControlPlane: cp,
		Executors: node.ExecutorMap{"test": realy.ExecutorFunc(func(ctx context.Context, execution realy.Execution) (realy.Result, error) {
			execution.Emit(ctx, "assistant.message.delta", map[string]string{"delta": "hello"})
			return realy.Result{Summary: "hello"}, nil
		})},
	}
	if _, err := worker.Register(ctx); err != nil {
		t.Fatal(err)
	}
	run, err := service.Submit(ctx, realy.Request{AgentID: "agent", IdempotencyKey: "lost-response", Runtime: realy.RuntimeRequirement{Provider: "test"}, Input: realy.Input{Prompt: "work"}})
	if err != nil {
		t.Fatal(err)
	}
	completed, err := worker.RunOnce(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != realy.RunSucceeded || cp.calls != 2 {
		t.Fatalf("status=%s calls=%d", completed.Status, cp.calls)
	}
	events, err := service.Events(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, event := range events {
		if event.Type == "assistant.message.delta" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("persisted deltas=%d", count)
	}
}

func (f failingEvents) AppendEvent(context.Context, string, string, string, string, any, ...string) error {
	return f.cause
}

func TestEventDeliveryFailureCannotCompleteRun(t *testing.T) {
	ctx := context.Background()
	service := controlplane.New(time.Second)
	cause := errors.New("event connection interrupted")
	worker := &node.Worker{
		Registration: controlplane.NodeRegistration{ID: "event-node", Runtimes: []controlplane.Runtime{{Provider: "test"}}, Capacity: 1},
		ControlPlane: failingEvents{ControlPlane: service, cause: cause},
		Executors: node.ExecutorMap{"test": realy.ExecutorFunc(func(ctx context.Context, execution realy.Execution) (realy.Result, error) {
			execution.Emit(ctx, "assistant.message.delta", map[string]string{"delta": "hello"})
			if ctx.Err() == nil {
				t.Error("failed delivery must cancel runtime execution")
			}
			// Even an adapter that returns success after cancellation cannot hide
			// the delivery failure from the host.
			return realy.Result{Summary: "done"}, nil
		})},
	}
	if _, err := worker.Register(ctx); err != nil {
		t.Fatal(err)
	}
	run, err := service.Submit(ctx, realy.Request{AgentID: "agent", IdempotencyKey: "event-failure", Runtime: realy.RuntimeRequirement{Provider: "test"}, Input: realy.Input{Prompt: "work"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := worker.RunOnce(ctx); !errors.Is(err, cause) {
		t.Fatalf("expected delivery error, got %v", err)
	}
	persisted, err := service.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if persisted.Status != realy.RunFailed {
		t.Fatalf("status = %s, want failed", persisted.Status)
	}
}

func TestWorkerRenewsLeaseDuringLongExecution(t *testing.T) {
	ctx := context.Background()
	service := controlplane.New(30 * time.Millisecond)
	worker := &node.Worker{
		Registration: controlplane.NodeRegistration{ID: "node", Runtimes: []controlplane.Runtime{{Provider: "slow"}}, Capacity: 1},
		ControlPlane: service,
		Executors: node.ExecutorMap{"slow": realy.ExecutorFunc(func(ctx context.Context, _ realy.Execution) (realy.Result, error) {
			select {
			case <-ctx.Done():
				return realy.Result{}, ctx.Err()
			case <-time.After(120 * time.Millisecond):
				return realy.Result{Summary: "done"}, nil
			}
		})},
	}
	if _, err := worker.Register(ctx); err != nil {
		t.Fatal(err)
	}
	_, err := service.Submit(ctx, realy.Request{AgentID: "agent", IdempotencyKey: "long", Runtime: realy.RuntimeRequirement{Provider: "slow"}, Input: realy.Input{Prompt: "work"}})
	if err != nil {
		t.Fatal(err)
	}
	completed, err := worker.RunOnce(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != realy.RunSucceeded || completed.Result == nil || completed.Result.Summary != "done" {
		t.Fatalf("unexpected completed run: %+v", completed)
	}
}

func TestWorkerStopsExecutionAndAcknowledgesCancellation(t *testing.T) {
	ctx := context.Background()
	service := controlplane.New(60 * time.Millisecond)
	started := make(chan struct{})
	worker := &node.Worker{
		Registration: controlplane.NodeRegistration{ID: "node", Runtimes: []controlplane.Runtime{{Provider: "blocking"}}, Capacity: 1},
		ControlPlane: service,
		Executors: node.ExecutorMap{"blocking": realy.ExecutorFunc(func(ctx context.Context, _ realy.Execution) (realy.Result, error) {
			close(started)
			<-ctx.Done()
			return realy.Result{}, ctx.Err()
		})},
	}
	if _, err := worker.Register(ctx); err != nil {
		t.Fatal(err)
	}
	submitted, err := service.Submit(ctx, realy.Request{AgentID: "agent", IdempotencyKey: "cancel-worker", Runtime: realy.RuntimeRequirement{Provider: "blocking"}, Input: realy.Input{Prompt: "work"}})
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		run realy.Run
		err error
	}
	done := make(chan result, 1)
	go func() {
		run, runErr := worker.RunOnce(ctx)
		done <- result{run: run, err: runErr}
	}()
	<-started
	if _, err := service.CancelRun(ctx, submitted.ID, controlplane.CancelRequest{Reason: "test cancellation", RequestedBy: "test"}); err != nil {
		t.Fatal(err)
	}
	select {
	case result := <-done:
		if result.err != nil {
			t.Fatal(result.err)
		}
		if result.run.Status != realy.RunCancelled {
			t.Fatalf("worker returned status %s", result.run.Status)
		}
	case <-time.After(time.Second):
		t.Fatal("worker did not stop after cancellation")
	}
	persisted, _ := service.GetRun(ctx, submitted.ID)
	if persisted.Status != realy.RunCancelled || persisted.Attempt.Status != realy.AttemptCancelled {
		t.Fatalf("unexpected persisted cancellation: %+v", persisted)
	}
}

func TestRunPoolUsesConfiguredCapacityConcurrently(t *testing.T) {
	service := controlplane.New(time.Second)
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	var mu sync.Mutex
	active, peak := 0, 0
	executor := realy.ExecutorFunc(func(ctx context.Context, _ realy.Execution) (realy.Result, error) {
		mu.Lock()
		active++
		if active > peak {
			peak = active
		}
		mu.Unlock()
		started <- struct{}{}
		select {
		case <-ctx.Done():
			return realy.Result{}, ctx.Err()
		case <-release:
		}
		mu.Lock()
		active--
		mu.Unlock()
		return realy.Result{Summary: "done"}, nil
	})
	worker := &node.Worker{
		Registration: controlplane.NodeRegistration{ID: "node", Runtimes: []controlplane.Runtime{{Provider: "parallel"}}, Capacity: 2},
		ControlPlane: service,
		Executors:    node.ExecutorMap{"parallel": executor},
	}
	ctx := context.Background()
	if _, err := worker.Register(ctx); err != nil {
		t.Fatal(err)
	}
	for index := range 2 {
		_, err := service.Submit(ctx, realy.Request{AgentID: "agent", IdempotencyKey: fmt.Sprintf("parallel-%d", index), Runtime: realy.RuntimeRequirement{Provider: "parallel"}, Input: realy.Input{Prompt: "work"}})
		if err != nil {
			t.Fatal(err)
		}
	}
	claimCtx, stopClaims := context.WithCancel(ctx)
	executionCtx, stopExecutions := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		worker.RunPool(claimCtx, executionCtx, time.Millisecond, func(err error) { t.Errorf("pool: %v", err) })
		close(done)
	}()
	for range 2 {
		select {
		case <-started:
		case <-time.After(time.Second):
			t.Fatal("capacity slots did not execute concurrently")
		}
	}
	close(release)
	stopClaims()
	select {
	case <-done:
	case <-time.After(time.Second):
		stopExecutions()
		t.Fatal("worker pool did not drain")
	}
	stopExecutions()
	mu.Lock()
	defer mu.Unlock()
	if peak != 2 {
		t.Fatalf("peak concurrency = %d, want 2", peak)
	}
}
