// Package sdk is the business-facing integration surface for Relay.
package sdk

import (
	"context"
	"encoding/json"
	"io"

	"github.com/KDF5000/relay"
	"github.com/KDF5000/relay/controlplane"
)

// Submitter is sufficient for applications that only dispatch work.
// Both Client and the HTTP transport implement it.
type Submitter interface {
	Submit(context.Context, relay.Request) (relay.Run, error)
}

type Runs interface {
	Submitter
	GetRun(context.Context, string) (relay.Run, error)
	CancelRun(context.Context, string, controlplane.CancelRequest) (relay.Run, error)
	ListRuns(context.Context, int) ([]relay.Run, error)
	SessionRuns(context.Context, string, int) ([]relay.Run, error)
	Attempts(context.Context, string) ([]relay.Attempt, error)
}

type Events interface {
	Events(context.Context, string) ([]relay.Event, error)
	StreamEvents(context.Context, string, int, func(relay.Event) error) error
}

type Artifacts interface {
	Artifacts(context.Context, string) ([]relay.Artifact, error)
	OpenArtifact(context.Context, string) (io.ReadCloser, error)
}

type Interactions interface {
	Interactions(context.Context, string) ([]relay.Interaction, error)
	ResolveInteraction(context.Context, string, json.RawMessage) (relay.Interaction, error)
}

// Backend composes the complete convenience client's capabilities. Business
// integrations should accept only the small interfaces they actually consume.
type Backend interface {
	Runs
	Events
	Artifacts
	Interactions
	Nodes(context.Context) ([]controlplane.Node, error)
}

func (c *Client) Artifacts(ctx context.Context, runID string) ([]relay.Artifact, error) {
	return c.backend.Artifacts(ctx, runID)
}
func (c *Client) OpenArtifact(ctx context.Context, artifactID string) (io.ReadCloser, error) {
	return c.backend.OpenArtifact(ctx, artifactID)
}
func (c *Client) ListRuns(ctx context.Context, limit int) ([]relay.Run, error) {
	return c.backend.ListRuns(ctx, limit)
}
func (c *Client) SessionRuns(ctx context.Context, sessionID string, limit int) ([]relay.Run, error) {
	return c.backend.SessionRuns(ctx, sessionID, limit)
}
func (c *Client) Attempts(ctx context.Context, runID string) ([]relay.Attempt, error) {
	return c.backend.Attempts(ctx, runID)
}
func (c *Client) Interactions(ctx context.Context, runID string) ([]relay.Interaction, error) {
	return c.backend.Interactions(ctx, runID)
}
func (c *Client) ResolveInteraction(ctx context.Context, id string, response json.RawMessage) (relay.Interaction, error) {
	return c.backend.ResolveInteraction(ctx, id, response)
}

type Client struct{ backend Backend }

func New(backend Backend) *Client { return &Client{backend: backend} }
func (c *Client) Submit(ctx context.Context, request relay.Request) (relay.Run, error) {
	return c.backend.Submit(ctx, request)
}
func (c *Client) Run(ctx context.Context, runID string) (relay.Run, error) {
	return c.backend.GetRun(ctx, runID)
}
func (c *Client) GetRun(ctx context.Context, runID string) (relay.Run, error) {
	return c.backend.GetRun(ctx, runID)
}
func (c *Client) Events(ctx context.Context, runID string) ([]relay.Event, error) {
	return c.backend.Events(ctx, runID)
}
func (c *Client) Nodes(ctx context.Context) ([]controlplane.Node, error) { return c.backend.Nodes(ctx) }
func (c *Client) CancelRun(ctx context.Context, runID string, request controlplane.CancelRequest) (relay.Run, error) {
	return c.backend.CancelRun(ctx, runID, request)
}
func (c *Client) StreamEvents(ctx context.Context, runID string, after int, handle func(relay.Event) error) error {
	return c.backend.StreamEvents(ctx, runID, after, handle)
}
