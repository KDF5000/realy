package postgres_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/KDF5000/relay"
	"github.com/KDF5000/relay/controlplane"
	controlplanepostgres "github.com/KDF5000/relay/controlplane/postgres"
	"github.com/KDF5000/relay/transport/httpapi"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestMultiNodeCrashRecoveryOverHTTP(t *testing.T) {
	store := openTestStore(t)
	service := controlplane.NewWithStorage(store, 80*time.Millisecond)
	server := httptest.NewServer(httpapi.NewHandler(service))
	defer server.Close()
	client := httpapi.NewClient(server.URL)
	ctx := context.Background()
	for _, id := range []string{"node-crash", "node-recovery"} {
		if _, err := client.RegisterNode(ctx, controlplane.NodeRegistration{ID: id, Capacity: 1, Runtimes: []controlplane.Runtime{{Provider: "mock", Version: "1.2.0", State: "healthy"}}}); err != nil {
			t.Fatal(err)
		}
	}
	run, err := client.Submit(ctx, relay.Request{AgentID: "agent", IdempotencyKey: "multi-node-crash", Runtime: relay.RuntimeRequirement{Provider: "mock", Version: "^1.0.0"}, Input: relay.Input{Prompt: "recover"}, Retry: relay.RetryPolicy{MaxAttempts: 2}})
	if err != nil {
		t.Fatal(err)
	}
	first, err := client.Claim(ctx, "node-crash")
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Start(ctx, first); err != nil {
		t.Fatal(err)
	}
	artifact, err := client.UploadArtifact(ctx, first, relay.Artifact{Type: "log", Name: "attempt.log"}, bytes.NewBufferString("before crash"))
	if err != nil {
		t.Fatal(err)
	}
	artifacts, err := client.Artifacts(ctx, run.ID)
	if err != nil || len(artifacts) != 1 || artifacts[0].ID != artifact.ID {
		t.Fatalf("artifacts = %+v, %v", artifacts, err)
	}
	request := relay.CapabilityRequest{Name: "issue.read", Version: "1", Resource: "MUL-1", Input: json.RawMessage(`{"id":"MUL-1"}`)}
	reservation, err := client.ReserveCapability(ctx, first, "read-once", "stable-hash", request)
	if err != nil {
		t.Fatal(err)
	}
	cached := relay.CapabilityResult{CallID: reservation.CallID, Output: json.RawMessage(`{"title":"cached"}`)}
	if err := client.FinishCapability(ctx, first, reservation, cached, ""); err != nil {
		t.Fatal(err)
	}
	time.Sleep(110 * time.Millisecond)
	if recovered, err := service.Reconcile(ctx, 10); err != nil || recovered != 1 {
		t.Fatalf("reconcile = %d, %v", recovered, err)
	}
	second, err := client.Claim(ctx, "node-recovery")
	if err != nil {
		t.Fatal(err)
	}
	if second.AttemptID == first.AttemptID {
		t.Fatal("expected a new attempt")
	}
	if err := client.Start(ctx, second); err != nil {
		t.Fatal(err)
	}
	replayed, err := client.ReserveCapability(ctx, second, "read-once", "stable-hash", request)
	if err != nil {
		t.Fatal(err)
	}
	if replayed.Execute || replayed.Result == nil || string(replayed.Result.Output) != string(cached.Output) {
		t.Fatalf("unexpected replay: %+v", replayed)
	}
	if err := client.Complete(ctx, second, relay.Result{Summary: "recovered"}); err != nil {
		t.Fatal(err)
	}
	attempts, err := client.Attempts(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(attempts) != 2 || attempts[0].Status != relay.AttemptLost || attempts[1].Status != relay.AttemptSucceeded {
		t.Fatalf("unexpected attempts: %+v", attempts)
	}
}

func TestPostgresControlPlaneLifecycleAndConcurrentClaim(t *testing.T) {
	store := openTestStore(t)
	service := controlplane.NewWithStorage(store, time.Minute)
	ctx := context.Background()
	for _, nodeID := range []string{"node-a", "node-b"} {
		if _, err := service.RegisterNode(ctx, controlplane.NodeRegistration{
			ID: nodeID, Capacity: 1,
			Runtimes:     []controlplane.Runtime{{Provider: "codex", Version: "test"}},
			Capabilities: []controlplane.Capability{{Name: "issue.read", Version: "1", Kind: "exec"}},
		}); err != nil {
			t.Fatal(err)
		}
	}
	request := relay.Request{
		AgentID: "agent", IdempotencyKey: "postgres-lifecycle",
		Runtime: relay.RuntimeRequirement{Provider: "codex"},
		Input:   relay.Input{Type: "task", Version: "1", Prompt: "work"},
		Capabilities: []relay.CapabilityGrant{{
			Name: "issue.read", Version: "1", Effect: "read", Resources: []string{"MUL-42"},
		}},
	}
	run, err := service.Submit(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	duplicate, err := service.Submit(ctx, request)
	if err != nil || duplicate.ID != run.ID {
		t.Fatalf("idempotent submit = %s, %v; want %s", duplicate.ID, err, run.ID)
	}

	type claimResult struct {
		assignment controlplane.Assignment
		err        error
	}
	results := make(chan claimResult, 2)
	var wait sync.WaitGroup
	for _, nodeID := range []string{"node-a", "node-b"} {
		wait.Add(1)
		go func() {
			defer wait.Done()
			assignment, claimErr := service.Claim(ctx, nodeID)
			results <- claimResult{assignment, claimErr}
		}()
	}
	wait.Wait()
	close(results)
	var assignment controlplane.Assignment
	successes, empty := 0, 0
	for result := range results {
		switch {
		case result.err == nil:
			successes++
			assignment = result.assignment
		case errors.Is(result.err, controlplane.ErrNoAssignment):
			empty++
		default:
			t.Fatalf("claim: %v", result.err)
		}
	}
	if successes != 1 || empty != 1 {
		t.Fatalf("claim results successes=%d empty=%d", successes, empty)
	}
	if err := service.Start(ctx, assignment); err != nil {
		t.Fatal(err)
	}
	if err := service.AppendEvent(ctx, assignment.RunID, assignment.AttemptID, assignment.LeaseToken, "runtime.test", map[string]string{"state": "ok"}); err != nil {
		t.Fatal(err)
	}
	if err := service.Complete(ctx, assignment, relay.Result{Summary: "persisted"}); err != nil {
		t.Fatal(err)
	}
	completed, err := service.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != relay.RunSucceeded || completed.Result == nil || completed.Result.Summary != "persisted" {
		t.Fatalf("unexpected persisted run: %+v", completed)
	}
	events, err := service.Events(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 8 {
		t.Fatalf("event count = %d, want 8", len(events))
	}
	for index, event := range events {
		if event.Sequence != index+1 {
			t.Fatalf("event %d sequence = %d", index, event.Sequence)
		}
	}
	tail, err := service.EventsAfter(ctx, run.ID, 6)
	if err != nil {
		t.Fatal(err)
	}
	if len(tail) != 2 || tail[0].Sequence != 7 || tail[1].Sequence != 8 {
		t.Fatalf("unexpected event cursor result: %+v", tail)
	}
}

func TestPostgresExpiredLeaseCanBeReclaimed(t *testing.T) {
	store := openTestStore(t)
	service := controlplane.NewWithStorage(store, 100*time.Millisecond)
	ctx := context.Background()
	_, err := service.RegisterNode(ctx, controlplane.NodeRegistration{ID: "node", Capacity: 1, Runtimes: []controlplane.Runtime{{Provider: "codex"}}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Submit(ctx, relay.Request{AgentID: "agent", IdempotencyKey: "expired", Runtime: relay.RuntimeRequirement{Provider: "codex"}, Input: relay.Input{Prompt: "work"}})
	if err != nil {
		t.Fatal(err)
	}
	first, err := service.Claim(ctx, "node")
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	second, err := service.Claim(ctx, "node")
	if err != nil {
		t.Fatal(err)
	}
	if first.LeaseToken == second.LeaseToken {
		t.Fatal("expired assignment reused its lease token")
	}
	events, err := service.Events(ctx, first.RunID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, event := range events {
		found = found || event.Type == "attempt.lease_expired"
	}
	if !found {
		t.Fatal("missing attempt.lease_expired event")
	}
}

func TestPostgresRecoversExpiredRunningAttemptAndFencesOldNode(t *testing.T) {
	store := openTestStore(t)
	service := controlplane.NewWithStorage(store, 100*time.Millisecond)
	ctx := context.Background()
	_, err := service.RegisterNode(ctx, controlplane.NodeRegistration{ID: "node", Capacity: 1, Runtimes: []controlplane.Runtime{{Provider: "codex"}}})
	if err != nil {
		t.Fatal(err)
	}
	run, err := service.Submit(ctx, relay.Request{
		AgentID: "agent", IdempotencyKey: "running-recovery",
		Runtime: relay.RuntimeRequirement{Provider: "codex"}, Input: relay.Input{Prompt: "work"},
		Retry: relay.RetryPolicy{MaxAttempts: 2, Backoff: "100ms"},
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err := service.Claim(ctx, "node")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Start(ctx, first); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	type reconcileResult struct {
		count int
		err   error
	}
	results := make(chan reconcileResult, 2)
	for range 2 {
		go func() {
			count, reconcileErr := service.Reconcile(ctx, 10)
			results <- reconcileResult{count: count, err: reconcileErr}
		}()
	}
	total := 0
	for range 2 {
		result := <-results
		if result.err != nil {
			t.Fatal(result.err)
		}
		total += result.count
	}
	if total != 1 {
		t.Fatalf("concurrent reconcilers recovered %d attempts, want 1", total)
	}
	retried, err := service.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if retried.Status != relay.RunQueued || retried.Attempt.Status != relay.AttemptQueued || retried.Attempt.Number != 2 {
		t.Fatalf("unexpected recovered run: %+v", retried)
	}
	if err := service.Complete(ctx, first, relay.Result{Summary: "stale"}); !errors.Is(err, controlplane.ErrInvalidLease) {
		t.Fatalf("stale completion = %v, want invalid lease", err)
	}
	if _, err := service.Claim(ctx, "node"); !errors.Is(err, controlplane.ErrNoAssignment) {
		t.Fatalf("attempt ignored retry backoff: %v", err)
	}
	time.Sleep(200 * time.Millisecond)
	second, err := service.Claim(ctx, "node")
	if err != nil {
		t.Fatal(err)
	}
	if second.AttemptID == first.AttemptID || second.LeaseToken == first.LeaseToken {
		t.Fatal("recovery reused the lost attempt or lease")
	}
	events, err := service.Events(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	foundLost, foundRetry := false, false
	for _, event := range events {
		foundLost = foundLost || event.Type == "attempt.lost"
		foundRetry = foundRetry || event.Type == "attempt.queued" && event.AttemptID == second.AttemptID
	}
	if !foundLost || !foundRetry {
		t.Fatalf("recovery events missing: lost=%v retry=%v", foundLost, foundRetry)
	}
}

func TestPostgresCancellationAndTimeout(t *testing.T) {
	store := openTestStore(t)
	service := controlplane.NewWithStorage(store, time.Second)
	ctx := context.Background()
	_, err := service.RegisterNode(ctx, controlplane.NodeRegistration{ID: "node", Capacity: 1, Runtimes: []controlplane.Runtime{{Provider: "codex"}}})
	if err != nil {
		t.Fatal(err)
	}
	running, err := service.Submit(ctx, relay.Request{AgentID: "agent", IdempotencyKey: "cancel-running", Runtime: relay.RuntimeRequirement{Provider: "codex"}, Input: relay.Input{Prompt: "work"}})
	if err != nil {
		t.Fatal(err)
	}
	assignment, err := service.Claim(ctx, "node")
	if err != nil {
		t.Fatal(err)
	}
	if err := service.Start(ctx, assignment); err != nil {
		t.Fatal(err)
	}
	pending, err := service.CancelRun(ctx, running.ID, controlplane.CancelRequest{Reason: "stop", RequestedBy: "integration-test"})
	if err != nil {
		t.Fatal(err)
	}
	if pending.Status != relay.RunCancelling {
		t.Fatalf("status = %s, want cancelling", pending.Status)
	}
	update, err := service.Renew(ctx, assignment)
	if err != nil {
		t.Fatal(err)
	}
	if !update.CancelRequested || update.CancelReason != "stop" {
		t.Fatalf("unexpected cancellation directive: %+v", update)
	}
	if err := service.Complete(ctx, assignment, relay.Result{Summary: "late"}); !errors.Is(err, controlplane.ErrRunCancelled) {
		t.Fatalf("late completion = %v, want run cancelled", err)
	}
	if err := service.AcknowledgeCancellation(ctx, assignment); err != nil {
		t.Fatal(err)
	}
	cancelled, err := service.GetRun(ctx, running.ID)
	if err != nil {
		t.Fatal(err)
	}
	if cancelled.Status != relay.RunCancelled || cancelled.Attempt.Status != relay.AttemptCancelled || cancelled.CancelledAt == nil {
		t.Fatalf("unexpected cancelled run: %+v", cancelled)
	}

	timed, err := service.Submit(ctx, relay.Request{AgentID: "agent", IdempotencyKey: "timeout-queued", Runtime: relay.RuntimeRequirement{Provider: "codex"}, Input: relay.Input{Prompt: "work"}, Timeout: "100ms"})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)
	if processed, err := service.Reconcile(ctx, 10); err != nil || processed != 1 {
		t.Fatalf("timeout reconcile = %d, %v", processed, err)
	}
	timed, err = service.GetRun(ctx, timed.ID)
	if err != nil {
		t.Fatal(err)
	}
	if timed.Status != relay.RunCancelled || timed.CancelReason != "run timeout exceeded" {
		t.Fatalf("unexpected timed out run: %+v", timed)
	}
}

func openTestStore(t *testing.T) *controlplanepostgres.Store {
	t.Helper()
	databaseURL := os.Getenv("RELAY_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("RELAY_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	schema := fmt.Sprintf("relay_test_%d", time.Now().UnixNano())
	identifier := pgx.Identifier{schema}.Sanitize()
	if _, err := admin.Exec(ctx, "CREATE SCHEMA "+identifier); err != nil {
		admin.Close()
		t.Fatal(err)
	}
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		admin.Close()
		t.Fatal(err)
	}
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		admin.Close()
		t.Fatal(err)
	}
	store := controlplanepostgres.New(pool)
	if err := store.Migrate(ctx); err != nil {
		pool.Close()
		_, _ = admin.Exec(ctx, "DROP SCHEMA "+identifier+" CASCADE")
		admin.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		pool.Close()
		_, _ = admin.Exec(context.Background(), "DROP SCHEMA "+identifier+" CASCADE")
		admin.Close()
	})
	return store
}
