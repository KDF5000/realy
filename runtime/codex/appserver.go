package codex

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/KDF5000/realy"
	runtimeprocess "github.com/KDF5000/realy/runtime/process"
	"github.com/KDF5000/realy/runtime/toolbridge"
)

type rpcMessage struct {
	ID     int             `json:"id,omitempty"`
	Method string          `json:"method,omitempty"`
	Params json.RawMessage `json:"params,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func executeAppServer(ctx context.Context, config Config, execution realy.Execution, fork ForkOptions) (result realy.Result, resultErr error) {
	binary := config.Binary
	if binary == "" {
		binary = fork.DefaultBinary
	}
	sandbox := config.Sandbox
	if sandbox == "" {
		sandbox = "workspace-write"
	}
	if sandbox != "read-only" && sandbox != "workspace-write" && sandbox != "danger-full-access" {
		return realy.Result{}, fmt.Errorf("realy %s: unsupported sandbox %q", fork.Name, sandbox)
	}
	if sandbox == "danger-full-access" && !config.AllowDangerousSandbox {
		return realy.Result{}, fmt.Errorf("realy %s: danger-full-access requires explicit AllowDangerousSandbox", fork.Name)
	}
	workDir := execution.WorkDir
	if workDir == "" {
		workDir = config.WorkDir
	}
	if workDir == "" {
		root := config.WorkRoot
		if root == "" {
			root = filepath.Join(os.TempDir(), "realy-runs")
		}
		workDir = filepath.Join(root, execution.RunID)
	}
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		return realy.Result{}, err
	}
	instructionPath, err := realy.MaterializeInstructions(workDir, fork.InstructionProvider, execution.Instructions)
	if err != nil {
		return realy.Result{}, err
	}
	resultDir := filepath.Join(workDir, ".realy")
	if err := os.MkdirAll(resultDir, 0o700); err != nil {
		return realy.Result{}, err
	}
	finalPath := filepath.Join(resultDir, fork.Name+"-last-message.txt")
	bridge, err := toolbridge.StartFile(execution.Capabilities, filepath.Join(resultDir, "tool-bridge"))
	if err != nil {
		return realy.Result{}, fmt.Errorf("realy %s: start tool bridge: %w", fork.Name, err)
	}
	defer bridge.Close()

	processCtx, stopProcess := context.WithCancel(ctx)
	defer stopProcess()
	command := exec.CommandContext(processCtx, binary, "app-server")
	runtimeprocess.Configure(command)
	command.Dir = workDir
	command.Env = runtimeEnv(config, bridge.Dir, bridge.Token, fork.PassEnv)
	stdin, err := command.StdinPipe()
	if err != nil {
		return realy.Result{}, err
	}
	stdout, err := command.StdoutPipe()
	if err != nil {
		return realy.Result{}, err
	}
	var stderr bytes.Buffer
	command.Stderr = &limitedBuffer{buffer: &stderr, remaining: 4 << 20}
	if execution.Emit != nil {
		execution.Emit(ctx, "runtime."+fork.Name+".started", map[string]any{"binary": binary, "protocol": "app-server", "work_dir": workDir, "sandbox": sandbox})
	}
	if err := command.Start(); err != nil {
		return realy.Result{}, fmt.Errorf("realy %s: start app-server: %w", fork.Name, err)
	}
	defer func() {
		stopProcess()
		waitErr := command.Wait()
		if resultErr == nil && waitErr != nil && ctx.Err() != nil {
			resultErr = ctx.Err()
		}
	}()

	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 64<<10), 8<<20)
	writeRequest := func(value any) error {
		return json.NewEncoder(stdin).Encode(value)
	}
	if err := writeRequest(map[string]any{"id": 1, "method": "initialize", "params": map[string]any{"clientInfo": map[string]string{"name": "realy", "version": "0.1"}, "capabilities": map[string]bool{"experimentalApi": true}}}); err != nil {
		return realy.Result{}, err
	}
	if _, err := waitRPCResponse(scanner, 1, nil); err != nil {
		return realy.Result{}, appServerError(fork.Name, err, stderr.String())
	}
	if err := writeRequest(map[string]any{"method": "initialized", "params": map[string]any{}}); err != nil {
		return realy.Result{}, err
	}
	threadParams := map[string]any{"cwd": workDir, "sandbox": sandbox, "approvalPolicy": "never", "ephemeral": config.Ephemeral}
	if config.Model != "" {
		threadParams["model"] = config.Model
	}
	if config.ServiceTier != "" {
		threadParams["serviceTier"] = config.ServiceTier
	}
	if err := writeRequest(map[string]any{"id": 2, "method": "thread/start", "params": threadParams}); err != nil {
		return realy.Result{}, err
	}
	threadResponse, err := waitRPCResponse(scanner, 2, func(message rpcMessage) { emitAppServerEvent(ctx, execution, fork.Name, message) })
	if err != nil {
		return realy.Result{}, appServerError(fork.Name, err, stderr.String())
	}
	var started struct {
		Thread struct {
			ID string `json:"id"`
		} `json:"thread"`
	}
	if err := json.Unmarshal(threadResponse, &started); err != nil || started.Thread.ID == "" {
		return realy.Result{}, fmt.Errorf("realy %s: invalid thread/start response", fork.Name)
	}
	threadID := started.Thread.ID
	if err := writeRequest(map[string]any{"id": 3, "method": "turn/start", "params": map[string]any{"threadId": threadID, "input": []map[string]string{{"type": "text", "text": execution.Instructions.Prompt}}}}); err != nil {
		return realy.Result{}, err
	}

	var message strings.Builder
	turnStarted := false
	for scanner.Scan() {
		var rpc rpcMessage
		if err := json.Unmarshal(scanner.Bytes(), &rpc); err != nil {
			continue
		}
		if rpc.ID == 3 {
			if rpc.Error != nil {
				return realy.Result{}, errors.New(rpc.Error.Message)
			}
			turnStarted = true
			continue
		}
		emitAppServerEvent(ctx, execution, fork.Name, rpc)
		if rpc.Method == "item/agentMessage/delta" {
			var params struct {
				Delta string `json:"delta"`
			}
			_ = json.Unmarshal(rpc.Params, &params)
			message.WriteString(params.Delta)
			if execution.Emit != nil && params.Delta != "" {
				execution.Emit(ctx, "assistant.message.delta", map[string]string{"delta": params.Delta})
			}
		}
		if rpc.Method == "item/completed" && message.Len() == 0 {
			if text := completedAgentMessage(rpc.Params); text != "" {
				message.WriteString(text)
				if execution.Emit != nil {
					execution.Emit(ctx, "assistant.message.completed", map[string]string{"text": text})
				}
			}
		}
		if rpc.Method == "turn/completed" {
			var params struct {
				Turn struct {
					Status string `json:"status"`
					Error  *struct {
						Message string `json:"message"`
					} `json:"error"`
				} `json:"turn"`
			}
			_ = json.Unmarshal(rpc.Params, &params)
			if params.Turn.Status != "completed" {
				cause := "turn " + params.Turn.Status
				if params.Turn.Error != nil && params.Turn.Error.Message != "" {
					cause = params.Turn.Error.Message
				}
				return realy.Result{}, errors.New(cause)
			}
			break
		}
	}
	if err := scanner.Err(); err != nil {
		return realy.Result{}, fmt.Errorf("realy %s: read app-server stream: %w", fork.Name, err)
	}
	if !turnStarted || message.Len() == 0 {
		return realy.Result{}, appServerError(fork.Name, errors.New("app-server ended without a final message"), stderr.String())
	}
	finalMessage := strings.TrimSpace(message.String())
	if err := os.WriteFile(finalPath, []byte(finalMessage), 0o600); err != nil {
		return realy.Result{}, err
	}
	output, _ := json.Marshal(map[string]any{"thread_id": threadID, "message": finalMessage, "work_dir": workDir})
	if execution.Emit != nil {
		execution.Emit(ctx, "runtime."+fork.Name+".completed", map[string]string{"thread_id": threadID})
	}
	return realy.Result{Summary: finalMessage, Output: output, Artifacts: []realy.Artifact{{Type: "instruction_file", Ref: instructionPath, Name: filepath.Base(instructionPath)}, {Type: fork.Name + "_final_message", Ref: finalPath, Name: filepath.Base(finalPath)}}}, nil
}

func waitRPCResponse(scanner *bufio.Scanner, id int, onNotification func(rpcMessage)) (json.RawMessage, error) {
	for scanner.Scan() {
		var message rpcMessage
		if err := json.Unmarshal(scanner.Bytes(), &message); err != nil {
			continue
		}
		if message.ID != id {
			if onNotification != nil {
				onNotification(message)
			}
			continue
		}
		if message.Error != nil {
			return nil, errors.New(message.Error.Message)
		}
		return message.Result, nil
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}
	return nil, errors.New("app-server closed the event stream")
}

func emitAppServerEvent(ctx context.Context, execution realy.Execution, provider string, message rpcMessage) {
	if execution.Emit == nil || message.Method == "" {
		return
	}
	eventType := strings.ReplaceAll(message.Method, "/", ".")
	var params any
	if len(message.Params) > 0 {
		_ = json.Unmarshal(message.Params, &params)
	}
	execution.Emit(ctx, "runtime."+provider+"."+eventType, params)
}

func completedAgentMessage(data json.RawMessage) string {
	var params struct {
		Item struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"item"`
	}
	_ = json.Unmarshal(data, &params)
	if params.Item.Type == "agentMessage" {
		return params.Item.Text
	}
	return ""
}

func appServerError(provider string, err error, stderr string) error {
	if detail := strings.TrimSpace(stderr); detail != "" {
		return fmt.Errorf("realy %s: %w: %s", provider, err, truncate(detail, 8192))
	}
	return fmt.Errorf("realy %s: %w", provider, err)
}
