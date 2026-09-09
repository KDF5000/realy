package httpapi_test

import (
	"context"
	"github.com/KDF5000/relay"
	"github.com/KDF5000/relay/controlplane"
	"github.com/KDF5000/relay/transport/httpapi"
	"net/http/httptest"
	"testing"
	"time"
)

func TestProducerEventDeduplication(t *testing.T) {
	ctx := context.Background()
	service := controlplane.New(time.Second)
	server := httptest.NewServer(httpapi.NewHandler(service))
	defer server.Close()
	client := httpapi.NewClient(server.URL)
	if _, err := client.RegisterNode(ctx, controlplane.NodeRegistration{ID: "dedup-node", Capacity: 1, Runtimes: []controlplane.Runtime{{Provider: "test"}}}); err != nil {
		t.Fatal(err)
	}
	run, err := client.Submit(ctx, relay.Request{AgentID: "agent", IdempotencyKey: "dedup", Runtime: relay.RuntimeRequirement{Provider: "test"}, Input: relay.Input{Prompt: "work"}})
	if err != nil {
		t.Fatal(err)
	}
	a, err := client.Claim(ctx, "dedup-node")
	if err != nil {
		t.Fatal(err)
	}
	if err := client.Start(ctx, a); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := client.AppendEvent(ctx, run.ID, a.AttemptID, a.LeaseToken, "assistant.message.delta", map[string]string{"delta": "hello"}, "event-1"); err != nil {
			t.Fatal(err)
		}
	}
	if err := client.AppendEvent(ctx, run.ID, a.AttemptID, a.LeaseToken, "assistant.message.delta", map[string]string{"delta": "changed"}, "event-1"); err == nil {
		t.Fatal("conflicting content accepted")
	}
	if err := client.AppendEvent(ctx, run.ID, a.AttemptID, "stale-token", "assistant.message.delta", map[string]string{"delta": "hello"}, "event-1"); err == nil {
		t.Fatal("stale lease accepted")
	}
	events, err := client.Events(ctx, run.ID)
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
		t.Fatalf("count=%d", count)
	}
}
