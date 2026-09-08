package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/KDF5000/realy"
	"github.com/KDF5000/realy/controlplane"
	"github.com/KDF5000/realy/transport/httpapi"
)

func TestConsoleAssetsAndHostSession(t *testing.T) {
	service := controlplane.New(time.Minute)
	auth := httpapi.StaticTokens{{Value: "host-secret", Scope: controlplane.AccessScope{Kind: controlplane.AccessHost, TenantID: "tenant", ProjectID: "project"}}}
	server := httptest.NewServer(httpapi.NewHandlerWithAuth(service, auth))
	defer server.Close()

	response, err := http.Get(server.URL + "/console/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	response.Body.Close()
	if response.StatusCode != http.StatusOK || !bytes.Contains(body, []byte("Realy Playground")) {
		t.Fatalf("console response status=%d body=%q", response.StatusCode, body)
	}

	unauthorized, err := http.Get(server.URL + "/v1/nodes")
	if err != nil {
		t.Fatal(err)
	}
	unauthorized.Body.Close()
	if unauthorized.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status = %d, want 401", unauthorized.StatusCode)
	}

	sessionResponse, err := http.Post(server.URL+"/v1/console/session", "application/json", bytes.NewBufferString(`{"token":"host-secret"}`))
	if err != nil {
		t.Fatal(err)
	}
	sessionResponse.Body.Close()
	if sessionResponse.StatusCode != http.StatusNoContent || len(sessionResponse.Cookies()) != 1 {
		t.Fatalf("session status=%d cookies=%v", sessionResponse.StatusCode, sessionResponse.Cookies())
	}
	cookie := sessionResponse.Cookies()[0]
	if cookie.Name != "realy_host_token" || !cookie.HttpOnly || cookie.SameSite != http.SameSiteStrictMode {
		t.Fatalf("unexpected session cookie: %+v", cookie)
	}

	request, _ := http.NewRequest(http.MethodGet, server.URL+"/v1/nodes", nil)
	request.AddCookie(cookie)
	authorized, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	authorized.Body.Close()
	if authorized.StatusCode != http.StatusOK {
		t.Fatalf("cookie-authenticated status = %d, want 200", authorized.StatusCode)
	}
}

func TestAuthenticatedIsolationArtifactsAndInteractions(t *testing.T) {
	service := controlplane.New(time.Minute)
	auth := httpapi.StaticTokens{{Value: "host-a", Scope: controlplane.AccessScope{Kind: controlplane.AccessHost, TenantID: "tenant-a", ProjectID: "project"}}, {Value: "host-b", Scope: controlplane.AccessScope{Kind: controlplane.AccessHost, TenantID: "tenant-b", ProjectID: "project"}}, {Value: "node", Scope: controlplane.AccessScope{Kind: controlplane.AccessNode, Subject: "node-a"}}}
	server := httptest.NewServer(httpapi.NewHandlerWithAuth(service, auth))
	defer server.Close()
	ctx := context.Background()
	hostA := httpapi.NewAuthenticatedClient(server.URL, "host-a")
	hostB := httpapi.NewAuthenticatedClient(server.URL, "host-b")
	node := httpapi.NewAuthenticatedClient(server.URL, "node")
	if _, err := node.RegisterNode(ctx, controlplane.NodeRegistration{ID: "node-a", Capacity: 1, Runtimes: []controlplane.Runtime{{Provider: "mock", State: "healthy"}}}); err != nil {
		t.Fatal(err)
	}
	request := realy.Request{AgentID: "agent", IdempotencyKey: "same-key", Runtime: realy.RuntimeRequirement{Provider: "mock"}, Input: realy.Input{Prompt: "work"}, SessionID: "session-1"}
	runA, err := hostA.Submit(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := hostB.GetRun(ctx, runA.ID); !errors.Is(err, controlplane.ErrNotFound) {
		t.Fatalf("cross-tenant read = %v", err)
	}
	a, err := node.Claim(ctx, "node-a")
	if err != nil {
		t.Fatal(err)
	}
	if err := node.Start(ctx, a); err != nil {
		t.Fatal(err)
	}
	artifact, err := node.UploadArtifact(ctx, a, realy.Artifact{Type: "log", Name: "output.txt", ContentType: "text/plain"}, bytes.NewBufferString("hello artifact"))
	if err != nil {
		t.Fatal(err)
	}
	reader, err := hostA.OpenArtifact(ctx, artifact.ID)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(reader)
	reader.Close()
	if string(body) != "hello artifact" {
		t.Fatalf("artifact = %q", body)
	}
	interaction, err := node.CreateInteraction(ctx, a, realy.InteractionRequest{Kind: realy.InteractionApproval, Prompt: "deploy?"})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := hostA.ResolveInteraction(ctx, interaction.ID, json.RawMessage(`{"approved":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if resolved.State != "resolved" {
		t.Fatalf("interaction = %+v", resolved)
	}
	runB, err := hostB.Submit(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if runA.ID == runB.ID || runA.TenantID != "tenant-a" || runB.TenantID != "tenant-b" {
		t.Fatalf("scope failure: %+v %+v", runA, runB)
	}
	sessionRuns, err := hostA.SessionRuns(ctx, "session-1", 10)
	if err != nil || len(sessionRuns) != 1 || sessionRuns[0].ID != runA.ID {
		t.Fatalf("session runs = %+v, %v", sessionRuns, err)
	}
}

func TestHostAndNodeProtocol(t *testing.T) {
	server := httptest.NewServer(httpapi.NewHandler(controlplane.New(time.Minute)))
	defer server.Close()
	client := httpapi.NewClient(server.URL)
	ctx := context.Background()
	if _, err := client.RegisterNode(ctx, controlplane.NodeRegistration{ID: "node", Runtimes: []controlplane.Runtime{{Provider: "mock"}}, Capacity: 1}); err != nil {
		t.Fatal(err)
	}
	run, err := client.Submit(ctx, realy.Request{AgentID: "agent", IdempotencyKey: "one", Runtime: realy.RuntimeRequirement{Provider: "mock", Model: "model-a"}, Input: realy.Input{Prompt: "work"}})
	if err != nil {
		t.Fatal(err)
	}
	assignment, err := client.Claim(ctx, "node")
	if err != nil {
		t.Fatal(err)
	}
	if assignment.Request.Runtime.Model != "model-a" || run.Runtime.Model != "model-a" {
		t.Fatalf("model override did not survive HTTP protocol: run=%q assignment=%q", run.Runtime.Model, assignment.Request.Runtime.Model)
	}
	if err := client.Start(ctx, assignment); err != nil {
		t.Fatal(err)
	}
	renewal, err := client.Renew(ctx, assignment)
	if err != nil {
		t.Fatal(err)
	}
	if !renewal.LeaseExpiresAt.After(assignment.LeaseExpiresAt) {
		t.Fatalf("renewed lease %s did not advance beyond %s", renewal.LeaseExpiresAt, assignment.LeaseExpiresAt)
	}
	stale := assignment
	stale.LeaseToken = "stale"
	if _, err := client.Renew(ctx, stale); !errors.Is(err, controlplane.ErrInvalidLease) {
		t.Fatalf("stale HTTP renewal = %v, want invalid lease", err)
	}
	if err := client.AppendEvent(ctx, assignment.RunID, assignment.AttemptID, assignment.LeaseToken, "executor.output", map[string]string{"text": "ok"}); err != nil {
		t.Fatal(err)
	}
	if err := client.Complete(ctx, assignment, realy.Result{Summary: "done"}); err != nil {
		t.Fatal(err)
	}
	completed, err := client.GetRun(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if completed.Status != realy.RunSucceeded {
		t.Fatalf("unexpected status: %s", completed.Status)
	}
	events, err := client.Events(ctx, run.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 8 {
		t.Fatalf("expected 8 events, got %d", len(events))
	}
	var streamed []realy.Event
	if err := client.StreamEvents(ctx, run.ID, 2, func(event realy.Event) error {
		streamed = append(streamed, event)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(streamed) != 6 || streamed[0].Sequence != 3 || streamed[len(streamed)-1].Sequence != 8 {
		t.Fatalf("unexpected resumed stream: %+v", streamed)
	}
}

func TestCancellationProtocol(t *testing.T) {
	service := controlplane.New(time.Second)
	server := httptest.NewServer(httpapi.NewHandler(service))
	defer server.Close()
	client := httpapi.NewClient(server.URL)
	ctx := context.Background()
	_, _ = client.RegisterNode(ctx, controlplane.NodeRegistration{ID: "node", Runtimes: []controlplane.Runtime{{Provider: "mock"}}, Capacity: 1})
	run, err := client.Submit(ctx, realy.Request{AgentID: "agent", IdempotencyKey: "cancel-http", Runtime: realy.RuntimeRequirement{Provider: "mock"}, Input: realy.Input{Prompt: "work"}})
	if err != nil {
		t.Fatal(err)
	}
	assignment, _ := client.Claim(ctx, "node")
	if err := client.Start(ctx, assignment); err != nil {
		t.Fatal(err)
	}
	pending, err := client.CancelRun(ctx, run.ID, controlplane.CancelRequest{Reason: "stop", RequestedBy: "test"})
	if err != nil {
		t.Fatal(err)
	}
	if pending.Status != realy.RunCancelling {
		t.Fatalf("status = %s, want cancelling", pending.Status)
	}
	update, err := client.Renew(ctx, assignment)
	if err != nil {
		t.Fatal(err)
	}
	if !update.CancelRequested {
		t.Fatal("renewal did not deliver cancellation")
	}
	if err := client.AcknowledgeCancellation(ctx, assignment); err != nil {
		t.Fatal(err)
	}
	cancelled, _ := client.GetRun(ctx, run.ID)
	if cancelled.Status != realy.RunCancelled {
		t.Fatalf("status = %s, want cancelled", cancelled.Status)
	}
}

func TestEventStreamDeliversNewEventsUntilTerminalState(t *testing.T) {
	service := controlplane.New(time.Second)
	server := httptest.NewServer(httpapi.NewHandler(service))
	defer server.Close()
	client := httpapi.NewClient(server.URL)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, _ = client.RegisterNode(ctx, controlplane.NodeRegistration{ID: "node", Runtimes: []controlplane.Runtime{{Provider: "mock"}}, Capacity: 1})
	run, err := client.Submit(ctx, realy.Request{AgentID: "agent", IdempotencyKey: "stream-live", Runtime: realy.RuntimeRequirement{Provider: "mock"}, Input: realy.Input{Prompt: "work"}})
	if err != nil {
		t.Fatal(err)
	}
	producer := make(chan error, 1)
	go func() {
		time.Sleep(50 * time.Millisecond)
		assignment, err := client.Claim(ctx, "node")
		if err == nil {
			err = client.Start(ctx, assignment)
		}
		if err == nil {
			err = client.Complete(ctx, assignment, realy.Result{Summary: "streamed"})
		}
		producer <- err
	}()
	var sequences []int
	if err := client.StreamEvents(ctx, run.ID, 0, func(event realy.Event) error {
		sequences = append(sequences, event.Sequence)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := <-producer; err != nil {
		t.Fatal(fmt.Errorf("produce streamed events: %w", err))
	}
	if len(sequences) != 7 {
		t.Fatalf("streamed sequences = %v, want 1..7", sequences)
	}
	for index, sequence := range sequences {
		if sequence != index+1 {
			t.Fatalf("streamed sequences out of order: %v", sequences)
		}
	}
}
