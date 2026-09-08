package toolbridge

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/KDF5000/realy"
)

const maxFileMessageBytes = 4 << 20

type fileRequest struct {
	Token string               `json:"token"`
	Call  realy.CapabilityCall `json:"call"`
}

type fileResponse struct {
	Result realy.CapabilityResult `json:"result"`
	Error  string                 `json:"error,omitempty"`
}

// FileBridge provides sandbox-compatible local IPC through a private mailbox
// inside the runtime work directory.
type FileBridge struct {
	Dir    string
	Token  string
	cancel context.CancelFunc
	done   chan struct{}
	invoke realy.CapabilityInvoker
}

func StartFile(invoker realy.CapabilityInvoker, dir string) (*FileBridge, error) {
	if invoker == nil {
		return nil, errors.New("realy tool bridge: capability invoker is required")
	}
	for _, child := range []string{"requests", "responses"} {
		if err := os.MkdirAll(filepath.Join(dir, child), 0o700); err != nil {
			return nil, err
		}
	}
	token, err := randomID()
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	bridge := &FileBridge{Dir: dir, Token: token, cancel: cancel, done: make(chan struct{}), invoke: invoker}
	go bridge.serve(ctx)
	return bridge, nil
}

func (b *FileBridge) Close() {
	b.cancel()
	<-b.done
}

func (b *FileBridge) serve(ctx context.Context) {
	defer close(b.done)
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			entries, err := os.ReadDir(filepath.Join(b.Dir, "requests"))
			if err != nil {
				continue
			}
			for _, entry := range entries {
				if entry.Type().IsRegular() && strings.HasSuffix(entry.Name(), ".json") {
					b.process(ctx, entry.Name())
				}
			}
		}
	}
}

func (b *FileBridge) process(ctx context.Context, name string) {
	requestPath := filepath.Join(b.Dir, "requests", name)
	data, err := os.ReadFile(requestPath)
	_ = os.Remove(requestPath)
	response := fileResponse{}
	if err != nil {
		response.Error = err.Error()
	} else if len(data) > maxFileMessageBytes {
		response.Error = "realy tool bridge: request exceeds size limit"
	} else {
		var request fileRequest
		if err := json.Unmarshal(data, &request); err != nil {
			response.Error = err.Error()
		} else if request.Token != b.Token {
			response.Error = "realy tool bridge: unauthorized"
		} else {
			response.Result, err = b.invoke.Call(ctx, request.Call)
			if err != nil {
				response.Error = err.Error()
			}
		}
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		return
	}
	responsePath := filepath.Join(b.Dir, "responses", name)
	temporary := responsePath + ".tmp"
	if err := os.WriteFile(temporary, encoded, 0o600); err == nil {
		_ = os.Rename(temporary, responsePath)
	}
}

func CallFile(ctx context.Context, dir, token string, call realy.CapabilityCall) (realy.CapabilityResult, error) {
	requestID, err := randomID()
	if err != nil {
		return realy.CapabilityResult{}, err
	}
	name := requestID + ".json"
	request, err := json.Marshal(fileRequest{Token: token, Call: call})
	if err != nil {
		return realy.CapabilityResult{}, err
	}
	requestPath := filepath.Join(dir, "requests", name)
	temporary := requestPath + ".tmp"
	if err := os.WriteFile(temporary, request, 0o600); err != nil {
		return realy.CapabilityResult{}, fmt.Errorf("realy tool bridge: write request: %w", err)
	}
	if err := os.Rename(temporary, requestPath); err != nil {
		_ = os.Remove(temporary)
		return realy.CapabilityResult{}, fmt.Errorf("realy tool bridge: publish request: %w", err)
	}
	responsePath := filepath.Join(dir, "responses", name)
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			_ = os.Remove(requestPath)
			return realy.CapabilityResult{}, ctx.Err()
		case <-ticker.C:
			data, err := os.ReadFile(responsePath)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}
			if err != nil {
				return realy.CapabilityResult{}, err
			}
			_ = os.Remove(responsePath)
			if len(data) > maxFileMessageBytes {
				return realy.CapabilityResult{}, errors.New("realy tool bridge: response exceeds size limit")
			}
			var response fileResponse
			if err := json.Unmarshal(data, &response); err != nil {
				return realy.CapabilityResult{}, err
			}
			if response.Error != "" {
				return realy.CapabilityResult{}, errors.New(response.Error)
			}
			return response.Result, nil
		}
	}
}

func randomID() (string, error) {
	var value [24]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(value[:]), nil
}
