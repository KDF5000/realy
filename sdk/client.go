// Package sdk is the business-facing integration surface for Realy.
package sdk

import (
	"context"
	"encoding/json"
	"io"

	"github.com/KDF5000/realy"
	"github.com/KDF5000/realy/controlplane"
)

type Backend interface {
	Submit(context.Context, realy.Request) (realy.Run, error)
	GetRun(context.Context, string) (realy.Run, error)
	Events(context.Context, string) ([]realy.Event, error)
	Nodes(context.Context) ([]controlplane.Node, error)
	CancelRun(context.Context, string, controlplane.CancelRequest) (realy.Run, error)
	StreamEvents(context.Context, string, int, func(realy.Event) error) error
	Artifacts(context.Context, string) ([]realy.Artifact, error)
	OpenArtifact(context.Context, string) (io.ReadCloser, error)
	ListRuns(context.Context, int) ([]realy.Run, error)
	SessionRuns(context.Context, string, int) ([]realy.Run, error)
	Attempts(context.Context, string) ([]realy.Attempt, error)
	Interactions(context.Context, string) ([]realy.Interaction, error)
	ResolveInteraction(context.Context, string, json.RawMessage) (realy.Interaction, error)
}

func (c *Client) Artifacts(ctx context.Context, runID string) ([]realy.Artifact, error) {
	return c.backend.Artifacts(ctx, runID)
}
func (c *Client) OpenArtifact(ctx context.Context, artifactID string) (io.ReadCloser, error) {
	return c.backend.OpenArtifact(ctx, artifactID)
}
func (c *Client) ListRuns(ctx context.Context, limit int) ([]realy.Run, error) {
	return c.backend.ListRuns(ctx, limit)
}
func (c *Client) SessionRuns(ctx context.Context, sessionID string, limit int) ([]realy.Run, error) {
	return c.backend.SessionRuns(ctx, sessionID, limit)
}
func (c *Client) Attempts(ctx context.Context, runID string) ([]realy.Attempt, error) {
	return c.backend.Attempts(ctx, runID)
}
func (c *Client) Interactions(ctx context.Context, runID string) ([]realy.Interaction, error) {
	return c.backend.Interactions(ctx, runID)
}
func (c *Client) ResolveInteraction(ctx context.Context, id string, response json.RawMessage) (realy.Interaction, error) {
	return c.backend.ResolveInteraction(ctx, id, response)
}

type Client struct{ backend Backend }

func New(backend Backend) *Client { return &Client{backend: backend} }
func (c *Client) Submit(ctx context.Context, request realy.Request) (realy.Run, error) {
	return c.backend.Submit(ctx, request)
}
func (c *Client) Run(ctx context.Context, runID string) (realy.Run, error) {
	return c.backend.GetRun(ctx, runID)
}
func (c *Client) Events(ctx context.Context, runID string) ([]realy.Event, error) {
	return c.backend.Events(ctx, runID)
}
func (c *Client) Nodes(ctx context.Context) ([]controlplane.Node, error) { return c.backend.Nodes(ctx) }
func (c *Client) CancelRun(ctx context.Context, runID string, request controlplane.CancelRequest) (realy.Run, error) {
	return c.backend.CancelRun(ctx, runID, request)
}
func (c *Client) StreamEvents(ctx context.Context, runID string, after int, handle func(realy.Event) error) error {
	return c.backend.StreamEvents(ctx, runID, after, handle)
}
