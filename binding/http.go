package binding

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/KDF5000/realy"
)

type HTTPProvider struct {
	Endpoint string
	Token    string
	Client   *http.Client
}

func (p HTTPProvider) Invoke(ctx context.Context, request realy.CapabilityRequest) (json.RawMessage, error) {
	body, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, p.Endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpRequest.Header.Set("Content-Type", "application/json")
	if p.Token != "" {
		httpRequest.Header.Set("Authorization", "Bearer "+p.Token)
	}
	client := p.Client
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	response, err := client.Do(httpRequest)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return nil, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, fmt.Errorf("realy: HTTP binding returned %s: %s", response.Status, responseBody)
	}
	var envelope struct {
		Output json.RawMessage `json:"output"`
		Error  string          `json:"error,omitempty"`
	}
	if err := json.Unmarshal(responseBody, &envelope); err != nil {
		return nil, fmt.Errorf("realy: invalid HTTP binding response: %w", err)
	}
	if envelope.Error != "" {
		return nil, fmt.Errorf("realy: HTTP binding: %s", envelope.Error)
	}
	return envelope.Output, nil
}
