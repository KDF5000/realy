// Package command adapts any non-interactive runtime CLI to Realy's Executor contract.
package command

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"

	"github.com/KDF5000/realy"
	runtimeprocess "github.com/KDF5000/realy/runtime/process"
	"github.com/KDF5000/realy/runtime/toolbridge"
)

type Executor struct {
	Command    string
	Args       []string
	Env        map[string]string
	InheritEnv bool
}

type input struct {
	RunID        string                     `json:"run_id"`
	AttemptID    string                     `json:"attempt_id"`
	AgentID      string                     `json:"agent_id"`
	Source       realy.Source               `json:"source"`
	Input        realy.Input                `json:"input"`
	Context      []realy.ContextItem        `json:"context,omitempty"`
	Instructions realy.CompiledInstructions `json:"instructions"`
}

func (e Executor) Execute(ctx context.Context, execution realy.Execution) (realy.Result, error) {
	if e.Command == "" {
		return realy.Result{}, fmt.Errorf("realy: runtime command is required")
	}
	bridge, err := toolbridge.Start(execution.Capabilities)
	if err != nil {
		return realy.Result{}, fmt.Errorf("realy: start tool bridge: %w", err)
	}
	defer bridge.Close()
	payload, err := json.Marshal(input{RunID: execution.RunID, AttemptID: execution.AttemptID, AgentID: execution.AgentID, Source: execution.Source, Input: execution.Input, Context: execution.Context, Instructions: execution.Instructions})
	if err != nil {
		return realy.Result{}, err
	}
	cmd := exec.CommandContext(ctx, e.Command, e.Args...)
	runtimeprocess.Configure(cmd)
	cmd.Dir = execution.WorkDir
	cmd.Stdin = bytes.NewReader(payload)
	if e.InheritEnv {
		cmd.Env = os.Environ()
	}
	for key, value := range e.Env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	cmd.Env = append(cmd.Env, "REALY_TOOL_URL="+bridge.URL(), "REALY_TOOL_TOKEN="+bridge.Token)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return realy.Result{}, fmt.Errorf("realy: runtime command failed: %w: %s", err, stderr.String())
	}
	var result realy.Result
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		return realy.Result{}, fmt.Errorf("realy: invalid runtime result: %w", err)
	}
	return result, nil
}
