package httpapi

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/KDF5000/realy"
	"github.com/KDF5000/realy/controlplane"
)

var ErrEventStreamInterrupted = errors.New("realy HTTP: event stream ended before a terminal event")

type Client struct {
	BaseURL string
	HTTP    *http.Client
	Token   string
}

func NewAuthenticatedClient(baseURL, token string) *Client {
	client := NewClient(baseURL)
	client.Token = token
	return client
}
func (c *Client) authorize(request *http.Request) {
	if c.Token != "" {
		request.Header.Set("Authorization", "Bearer "+c.Token)
	}
}

func NewClient(baseURL string) *Client {
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), HTTP: &http.Client{Timeout: 30 * time.Second}}
}

func (c *Client) Submit(ctx context.Context, request realy.Request) (realy.Run, error) {
	var out realy.Run
	err := c.do(ctx, http.MethodPost, "/v1/runs", request, &out)
	return out, err
}
func (c *Client) GetRun(ctx context.Context, runID string) (realy.Run, error) {
	var out realy.Run
	err := c.do(ctx, http.MethodGet, "/v1/runs/"+url.PathEscape(runID), nil, &out)
	return out, err
}
func (c *Client) ListRuns(ctx context.Context, limit int) ([]realy.Run, error) {
	var out []realy.Run
	err := c.do(ctx, http.MethodGet, "/v1/runs?limit="+strconv.Itoa(limit), nil, &out)
	return out, err
}
func (c *Client) SessionRuns(ctx context.Context, sessionID string, limit int) ([]realy.Run, error) {
	var out []realy.Run
	err := c.do(ctx, http.MethodGet, "/v1/sessions/"+url.PathEscape(sessionID)+"/runs?limit="+strconv.Itoa(limit), nil, &out)
	return out, err
}
func (c *Client) Attempts(ctx context.Context, runID string) ([]realy.Attempt, error) {
	var out []realy.Attempt
	err := c.do(ctx, http.MethodGet, "/v1/runs/"+url.PathEscape(runID)+"/attempts", nil, &out)
	return out, err
}
func (c *Client) Interactions(ctx context.Context, runID string) ([]realy.Interaction, error) {
	var out []realy.Interaction
	err := c.do(ctx, http.MethodGet, "/v1/runs/"+url.PathEscape(runID)+"/interactions", nil, &out)
	return out, err
}
func (c *Client) ResolveInteraction(ctx context.Context, id string, response json.RawMessage) (realy.Interaction, error) {
	var out realy.Interaction
	err := c.do(ctx, http.MethodPost, "/v1/interactions/"+url.PathEscape(id)+"/resolve", map[string]any{"response": response}, &out)
	return out, err
}
func (c *Client) CreateInteraction(ctx context.Context, a controlplane.Assignment, request realy.InteractionRequest) (realy.Interaction, error) {
	var out realy.Interaction
	err := c.do(ctx, http.MethodPost, "/v1/attempts/"+url.PathEscape(a.AttemptID)+"/interactions", map[string]any{"assignment": a, "request": request}, &out)
	return out, err
}
func (c *Client) GetInteraction(ctx context.Context, a controlplane.Assignment, id string) (realy.Interaction, error) {
	path := "/v1/attempts/" + url.PathEscape(a.AttemptID) + "/interactions/" + url.PathEscape(id) + "?run_id=" + url.QueryEscape(a.RunID)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return realy.Interaction{}, err
	}
	request.Header.Set("X-Realy-Lease-Token", a.LeaseToken)
	c.authorize(request)
	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	response, err := client.Do(request)
	if err != nil {
		return realy.Interaction{}, err
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return realy.Interaction{}, fmt.Errorf("realy HTTP %s: %s", response.Status, strings.TrimSpace(string(body)))
	}
	var out realy.Interaction
	err = json.Unmarshal(body, &out)
	return out, err
}
func (c *Client) Events(ctx context.Context, runID string) ([]realy.Event, error) {
	var out []realy.Event
	err := c.do(ctx, http.MethodGet, "/v1/runs/"+url.PathEscape(runID)+"/events", nil, &out)
	return out, err
}
func (c *Client) Artifacts(ctx context.Context, runID string) ([]realy.Artifact, error) {
	var out []realy.Artifact
	err := c.do(ctx, http.MethodGet, "/v1/runs/"+url.PathEscape(runID)+"/artifacts", nil, &out)
	return out, err
}
func (c *Client) OpenArtifact(ctx context.Context, artifactID string) (io.ReadCloser, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/v1/artifacts/"+url.PathEscape(artifactID), nil)
	if err != nil {
		return nil, err
	}
	c.authorize(request)
	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	response, err := client.Do(request)
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		defer response.Body.Close()
		body, _ := io.ReadAll(io.LimitReader(response.Body, 8<<20))
		return nil, fmt.Errorf("realy HTTP %s: %s", response.Status, strings.TrimSpace(string(body)))
	}
	return response.Body, nil
}
func (c *Client) UploadArtifact(ctx context.Context, assignment controlplane.Assignment, artifact realy.Artifact, reader io.Reader) (realy.Artifact, error) {
	query := url.Values{"run_id": {assignment.RunID}, "type": {artifact.Type}, "name": {artifact.Name}}
	path := "/v1/attempts/" + url.PathEscape(assignment.AttemptID) + "/artifacts?" + query.Encode()
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+path, reader)
	if err != nil {
		return realy.Artifact{}, err
	}
	request.Header.Set("X-Realy-Lease-Token", assignment.LeaseToken)
	c.authorize(request)
	if artifact.ContentType != "" {
		request.Header.Set("Content-Type", artifact.ContentType)
	}
	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	response, err := client.Do(request)
	if err != nil {
		return realy.Artifact{}, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	if err != nil {
		return realy.Artifact{}, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return realy.Artifact{}, fmt.Errorf("realy HTTP %s: %s", response.Status, strings.TrimSpace(string(body)))
	}
	var out realy.Artifact
	if err := json.Unmarshal(body, &out); err != nil {
		return realy.Artifact{}, err
	}
	return out, nil
}
func (c *Client) ReserveCapability(ctx context.Context, a controlplane.Assignment, key, hash string, request realy.CapabilityRequest) (realy.CapabilityReservation, error) {
	var out realy.CapabilityReservation
	err := c.do(ctx, http.MethodPost, "/v1/attempts/"+url.PathEscape(a.AttemptID)+"/capability-calls/reserve", map[string]any{"assignment": a, "idempotency_key": key, "request_hash": hash, "request": request}, &out)
	return out, err
}
func (c *Client) FinishCapability(ctx context.Context, a controlplane.Assignment, reservation realy.CapabilityReservation, result realy.CapabilityResult, cause string) error {
	return c.do(ctx, http.MethodPost, "/v1/attempts/"+url.PathEscape(a.AttemptID)+"/capability-calls/"+url.PathEscape(reservation.CallID)+"/finish", map[string]any{"assignment": a, "reservation": reservation, "result": result, "error": cause}, nil)
}
func (c *Client) StreamEvents(ctx context.Context, runID string, after int, handle func(realy.Event) error) error {
	path := "/v1/runs/" + url.PathEscape(runID) + "/events/stream?after=" + strconv.Itoa(after)
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "text/event-stream")
	c.authorize(request)
	request.Header.Set("Last-Event-ID", strconv.Itoa(after))
	client := c.HTTP
	if client == nil {
		client = &http.Client{}
	} else if client.Timeout != 0 {
		streamClient := *client
		streamClient.Timeout = 0
		client = &streamClient
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, readErr := io.ReadAll(io.LimitReader(response.Body, 8<<20))
		if readErr != nil {
			return readErr
		}
		if response.StatusCode == http.StatusNotFound {
			return fmt.Errorf("%w: %s", controlplane.ErrNotFound, strings.TrimSpace(string(body)))
		}
		return fmt.Errorf("realy HTTP %s: %s", response.Status, strings.TrimSpace(string(body)))
	}
	scanner := bufio.NewScanner(response.Body)
	scanner.Buffer(make([]byte, 64<<10), 2<<20)
	var data bytes.Buffer
	terminal := false
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			if data.Len() == 0 {
				continue
			}
			var event realy.Event
			if err := json.Unmarshal(data.Bytes(), &event); err != nil {
				return fmt.Errorf("decode Realy event stream: %w", err)
			}
			data.Reset()
			if event.Sequence > after {
				after = event.Sequence
				if err := handle(event); err != nil {
					return err
				}
				terminal = terminal || event.Type == "run.succeeded" || event.Type == "run.failed" || event.Type == "run.cancelled"
			}
			continue
		}
		if strings.HasPrefix(line, "data:") {
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if !terminal {
		return ErrEventStreamInterrupted
	}
	return nil
}
func (c *Client) CancelRun(ctx context.Context, runID string, request controlplane.CancelRequest) (realy.Run, error) {
	var out realy.Run
	err := c.do(ctx, http.MethodPost, "/v1/runs/"+url.PathEscape(runID)+"/cancel", request, &out)
	return out, err
}
func (c *Client) Nodes(ctx context.Context) ([]controlplane.Node, error) {
	var out []controlplane.Node
	err := c.do(ctx, http.MethodGet, "/v1/nodes", nil, &out)
	return out, err
}
func (c *Client) RegisterNode(ctx context.Context, value controlplane.NodeRegistration) (controlplane.Node, error) {
	var out controlplane.Node
	err := c.do(ctx, http.MethodPost, "/v1/nodes/register", value, &out)
	return out, err
}
func (c *Client) Heartbeat(ctx context.Context, nodeID string) (controlplane.Node, error) {
	var out controlplane.Node
	err := c.do(ctx, http.MethodPost, "/v1/nodes/"+url.PathEscape(nodeID)+"/heartbeat", map[string]any{}, &out)
	return out, err
}
func (c *Client) Claim(ctx context.Context, nodeID string) (controlplane.Assignment, error) {
	var out controlplane.Assignment
	err := c.do(ctx, http.MethodPost, "/v1/nodes/"+url.PathEscape(nodeID)+"/claim", map[string]any{}, &out)
	return out, err
}
func (c *Client) Start(ctx context.Context, value controlplane.Assignment) error {
	return c.do(ctx, http.MethodPost, "/v1/attempts/"+url.PathEscape(value.AttemptID)+"/start", value, nil)
}
func (c *Client) Renew(ctx context.Context, value controlplane.Assignment) (controlplane.LeaseUpdate, error) {
	var out controlplane.LeaseUpdate
	err := c.do(ctx, http.MethodPost, "/v1/attempts/"+url.PathEscape(value.AttemptID)+"/renew", value, &out)
	return out, err
}
func (c *Client) AppendEvent(ctx context.Context, runID, attemptID, lease, eventType string, data any, eventIDs ...string) error {
	id := ""
	if len(eventIDs) > 0 {
		id = eventIDs[0]
	}
	return c.do(ctx, http.MethodPost, "/v1/attempts/"+url.PathEscape(attemptID)+"/events", map[string]any{"run_id": runID, "lease_token": lease, "type": eventType, "data": data, "event_id": id}, nil)
}
func (c *Client) Complete(ctx context.Context, value controlplane.Assignment, result realy.Result) error {
	return c.do(ctx, http.MethodPost, "/v1/attempts/"+url.PathEscape(value.AttemptID)+"/complete", map[string]any{"assignment": value, "result": result}, nil)
}
func (c *Client) Fail(ctx context.Context, value controlplane.Assignment, cause string) error {
	return c.do(ctx, http.MethodPost, "/v1/attempts/"+url.PathEscape(value.AttemptID)+"/fail", map[string]any{"assignment": value, "error": cause}, nil)
}
func (c *Client) AcknowledgeCancellation(ctx context.Context, value controlplane.Assignment) error {
	return c.do(ctx, http.MethodPost, "/v1/attempts/"+url.PathEscape(value.AttemptID)+"/cancelled", value, nil)
}

func (c *Client) do(ctx context.Context, method, path string, input, output any) error {
	var body io.Reader
	if input != nil {
		encoded, err := json.Marshal(input)
		if err != nil {
			return err
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, method, c.BaseURL+path, body)
	if err != nil {
		return err
	}
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	c.authorize(request)
	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNoContent {
		if strings.Contains(path, "/claim") {
			return controlplane.ErrNoAssignment
		}
		return nil
	}
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 8<<20))
	if err != nil {
		return err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var failure map[string]string
		_ = json.Unmarshal(responseBody, &failure)
		message := failure["error"]
		if message == "" {
			message = string(responseBody)
		}
		var protocolErr error
		switch response.StatusCode {
		case http.StatusNotFound:
			if strings.HasPrefix(message, controlplane.ErrNotFound.Error()) {
				protocolErr = controlplane.ErrNotFound
			}
		case http.StatusUnauthorized:
			// Authentication failures (or a proxy's 401) do not establish that
			// the attempt lease is stale. Durable pending events must be retained.
			if strings.HasPrefix(message, controlplane.ErrInvalidLease.Error()) {
				protocolErr = controlplane.ErrInvalidLease
			}
		case http.StatusConflict:
			if strings.HasPrefix(message, controlplane.ErrInvalidTransition.Error()) {
				protocolErr = controlplane.ErrInvalidTransition
			}
		case http.StatusGone:
			if strings.HasPrefix(message, controlplane.ErrRunCancelled.Error()) {
				protocolErr = controlplane.ErrRunCancelled
			}
		}
		if protocolErr != nil {
			return fmt.Errorf("%w: %s", protocolErr, message)
		}
		return fmt.Errorf("realy HTTP %s: %s", response.Status, message)
	}
	if output == nil || len(responseBody) == 0 {
		return nil
	}
	if err := json.Unmarshal(responseBody, output); err != nil {
		return fmt.Errorf("decode Realy response: %w", err)
	}
	return nil
}
