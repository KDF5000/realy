package relay_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/KDF5000/relay"
)

func TestInstructionMaterializationPreservesHostContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "AGENTS.md")
	if err := os.WriteFile(path, []byte("# Host-owned rules\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	compiled, err := (relay.DefaultInstructionCompiler{}).Compile(
		relay.Input{Prompt: "base"},
		relay.InstructionBundle{
			Workspace: []relay.InstructionFragment{{Title: "Stable", Content: "keep this"}},
			Turn:      []relay.InstructionFragment{{Title: "Turn", Content: "prompt only"}},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := relay.MaterializeInstructions(dir, "codex", compiled); err != nil {
		t.Fatal(err)
	}
	if _, err := relay.MaterializeInstructions(dir, "codex", compiled); err != nil {
		t.Fatal(err)
	}
	content, _ := os.ReadFile(path)
	text := string(content)
	if !strings.Contains(text, "Host-owned rules") || !strings.Contains(text, "keep this") {
		t.Fatalf("missing stable content: %s", text)
	}
	if strings.Contains(text, "prompt only") || !strings.Contains(compiled.Prompt, "prompt only") {
		t.Fatalf("turn instruction leaked into stable file: %s", text)
	}
	if strings.Count(text, "BEGIN RELAY-RUNTIME") != 1 {
		t.Fatalf("managed block duplicated: %s", text)
	}
}
