package codex_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/KDF5000/realy"
	runtimecodex "github.com/KDF5000/realy/runtime/codex"
)

func TestExecutorUsesCodexExecJSONLAndFinalMessage(t *testing.T) {
	dir := t.TempDir()
	toolDir := filepath.Join(dir, "tools")
	if err := os.MkdirAll(toolDir, 0o755); err != nil {
		t.Fatal(err)
	}
	tool := filepath.Join(toolDir, "realy-tool")
	if err := os.WriteFile(tool, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	fake := filepath.Join(dir, "codex")
	script := `#!/bin/sh
out=""
model=""
while [ "$#" -gt 0 ]; do
  if [ "$1" = "--output-last-message" ]; then out="$2"; shift 2; continue; fi
  if [ "$1" = "--model" ]; then model="$2"; shift 2; continue; fi
  shift
done
cat >/dev/null
command -v realy-tool >/dev/null || exit 9
[ -n "$REALY_TOOL_DIR" ] || exit 10
[ -n "$REALY_TOOL_TOKEN" ] || exit 11
[ -z "$MULTICA_TOKEN" ] || exit 12
[ "$REALY_TEST_ALLOWED" = "allowed" ] || exit 13
[ "$model" = "per-agent-model" ] || exit 14
printf '%s\n' '{"type":"thread.started","thread_id":"thread-real"}'
printf '%s\n' '{"type":"item.completed","item":{"type":"agent_message","text":"done"}}'
printf '%s' 'Codex completed the real adapter contract' > "$out"
`
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	events := []string{}
	t.Setenv("MULTICA_TOKEN", "must-not-reach-agent")
	t.Setenv("REALY_TEST_ALLOWED", "allowed")
	executor := runtimecodex.Executor{Config: runtimecodex.Config{Binary: fake, ToolDir: toolDir, PassEnv: []string{"REALY_TEST_ALLOWED"}, Model: "node-default", WorkRoot: dir, Ephemeral: true}}
	result, err := executor.Execute(context.Background(), realy.Execution{RunID: "run-1", AgentID: "agent", Runtime: realy.RuntimeRequirement{Provider: "codex", Model: "per-agent-model"}, Input: realy.Input{Prompt: "do work"}, Instructions: realy.CompiledInstructions{Stable: "stable rules\n", Prompt: "do work\n"}, Capabilities: realy.NewCapabilityInvoker(realy.CapabilityInvokerOptions{}), Emit: func(_ context.Context, eventType string, _ any) { events = append(events, eventType) }})
	if err != nil {
		t.Fatal(err)
	}
	if result.Summary != "Codex completed the real adapter contract" {
		t.Fatalf("unexpected summary: %q", result.Summary)
	}
	if len(events) != 5 || events[1] != "runtime.codex.thread.started" || events[2] != "runtime.codex.item.completed" || events[3] != "assistant.message.completed" {
		t.Fatalf("unexpected events: %v", events)
	}
	content, err := os.ReadFile(filepath.Join(dir, "run-1", "AGENTS.md"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(content), "stable rules") {
		t.Fatalf("instructions not materialized: %s", content)
	}
}

func TestDangerousSandboxRequiresExplicitOptIn(t *testing.T) {
	executor := runtimecodex.Executor{Config: runtimecodex.Config{Sandbox: "danger-full-access"}}
	_, err := executor.Execute(context.Background(), realy.Execution{})
	if err == nil || !strings.Contains(err.Error(), "explicit") {
		t.Fatalf("expected safety error, got %v", err)
	}
}

func TestProbeModelsReadsAppServerCatalog(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "codex")
	script := `#!/bin/sh
[ "$1" = "app-server" ] || exit 8
read -r initialize
printf '%s\n' '{"id":1,"result":{"userAgent":"fake"}}'
read -r initialized
read -r model_list
printf '%s\n' '{"id":2,"result":{"data":[{"id":"model-a","model":"model-a","displayName":"Model A","description":"Fast model","hidden":false,"isDefault":true}],"nextCursor":null}}'
`
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	models, err := runtimecodex.ProbeModels(context.Background(), runtimecodex.Config{Binary: fake}, runtimecodex.ForkOptions{Name: "codex", DefaultBinary: "codex"})
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 1 || models[0].ID != "model-a" || models[0].DisplayName != "Model A" || !models[0].Default {
		t.Fatalf("models = %#v", models)
	}
}

func TestAppServerStreamsAssistantDeltas(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "codex")
	script := `#!/bin/sh
[ "$1" = "app-server" ] || exit 8
read -r initialize
printf '%s\n' '{"id":1,"result":{"userAgent":"fake"}}'
read -r initialized
read -r thread_start
printf '%s\n' '{"id":2,"result":{"thread":{"id":"thread-stream"}}}'
read -r turn_start
printf '%s\n' '{"id":3,"result":{"turn":{"id":"turn-1"}}}'
printf '%s\n' '{"method":"item/agentMessage/delta","params":{"threadId":"thread-stream","turnId":"turn-1","itemId":"item-1","delta":"hello "}}'
printf '%s\n' '{"method":"item/agentMessage/delta","params":{"threadId":"thread-stream","turnId":"turn-1","itemId":"item-1","delta":"world"}}'
printf '%s\n' '{"method":"turn/completed","params":{"threadId":"thread-stream","turn":{"id":"turn-1","items":[],"status":"completed"}}}'
`
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	var deltas []string
	executor := runtimecodex.Executor{Config: runtimecodex.Config{Binary: fake, Protocol: "app-server", WorkRoot: dir, Ephemeral: true}}
	result, err := executor.Execute(context.Background(), realy.Execution{RunID: "run-stream", Instructions: realy.CompiledInstructions{Stable: "rules", Prompt: "say hello"}, Capabilities: realy.NewCapabilityInvoker(realy.CapabilityInvokerOptions{}), Emit: func(_ context.Context, event string, data any) {
		if event == "assistant.message.delta" {
			value := data.(map[string]string)
			deltas = append(deltas, value["delta"])
		}
	}})
	if err != nil {
		t.Fatal(err)
	}
	if result.Summary != "hello world" || strings.Join(deltas, "") != "hello world" {
		t.Fatalf("result=%q deltas=%q", result.Summary, deltas)
	}
}

func TestAppServerRejectsPartialMessageWithoutTurnCompletion(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "codex")
	script := `#!/bin/sh
[ "$1" = "app-server" ] || exit 8
read -r initialize
printf '%s\n' '{"id":1,"result":{"userAgent":"fake"}}'
read -r initialized
read -r thread_start
printf '%s\n' '{"id":2,"result":{"thread":{"id":"thread-partial"}}}'
read -r turn_start
printf '%s\n' '{"id":3,"result":{"turn":{"id":"turn-partial"}}}'
printf '%s\n' '{"method":"item/agentMessage/delta","params":{"delta":"partial output"}}'
`
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	var partial string
	executor := runtimecodex.Executor{Config: runtimecodex.Config{Binary: fake, Protocol: "app-server", WorkRoot: dir, Ephemeral: true}}
	_, err := executor.Execute(context.Background(), realy.Execution{RunID: "run-partial", Instructions: realy.CompiledInstructions{Prompt: "work"}, Capabilities: realy.NewCapabilityInvoker(realy.CapabilityInvokerOptions{}), Emit: func(_ context.Context, event string, data any) {
		if event == "assistant.message.delta" {
			partial += data.(map[string]string)["delta"]
		}
	}})
	if err == nil || !strings.Contains(err.Error(), "before turn/completed") {
		t.Fatalf("expected incomplete turn error, got %v", err)
	}
	if partial != "partial output" {
		t.Fatalf("partial output = %q", partial)
	}
}
