package node_test

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/KDF5000/realy"
	"github.com/KDF5000/realy/controlplane"
	"github.com/KDF5000/realy/node"
)

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
