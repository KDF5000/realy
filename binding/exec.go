package binding

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"

	"github.com/KDF5000/realy"
	runtimeprocess "github.com/KDF5000/realy/runtime/process"
)

type Exec struct {
	Command        string
	Args           []string
	Env            map[string]string
	InheritEnv     bool
	MaxOutputBytes int64
}

type execResponse struct {
	Output json.RawMessage `json:"output"`
	Error  string          `json:"error,omitempty"`
}

type ExecProvider struct{ Config Exec }

func (p ExecProvider) Invoke(ctx context.Context, request realy.CapabilityRequest) (json.RawMessage, error) {
	config := p.Config
	if config.Command == "" {
		return nil, fmt.Errorf("realy: exec binding command is required")
	}
	input, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	command := exec.CommandContext(ctx, config.Command, config.Args...)
	runtimeprocess.Configure(command)
	command.Stdin = bytes.NewReader(input)
	if config.InheritEnv {
		command.Env = os.Environ()
	}
	for name, value := range config.Env {
		command.Env = append(command.Env, name+"="+value)
	}
	var stdout, stderr bytes.Buffer
	limit := config.MaxOutputBytes
	if limit <= 0 {
		limit = 4 << 20
	}
	command.Stdout = &limitedWriter{writer: &stdout, remaining: limit}
	command.Stderr = &limitedWriter{writer: &stderr, remaining: limit}
	if err := command.Run(); err != nil {
		return nil, fmt.Errorf("realy: exec binding failed: %w: %s", err, stderr.String())
	}
	var response execResponse
	if err := json.Unmarshal(stdout.Bytes(), &response); err != nil {
		return nil, fmt.Errorf("realy: invalid exec binding response: %w", err)
	}
	if response.Error != "" {
		return nil, fmt.Errorf("realy: exec binding: %s", response.Error)
	}
	return response.Output, nil
}

type limitedWriter struct {
	writer    io.Writer
	remaining int64
}

func (w *limitedWriter) Write(value []byte) (int, error) {
	if int64(len(value)) > w.remaining {
		return 0, errors.New("realy: binding output limit exceeded")
	}
	n, err := w.writer.Write(value)
	w.remaining -= int64(n)
	return n, err
}
