package workspace_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/KDF5000/relay"
	"github.com/KDF5000/relay/workspace"
)

func TestGitWorkspaceUsesMirrorAndCleansWorktree(t *testing.T) {
	source := t.TempDir()
	runGit(t, source, "init")
	runGit(t, source, "config", "user.email", "relay@example.test")
	runGit(t, source, "config", "user.name", "Relay Test")
	if err := os.WriteFile(filepath.Join(source, "README.md"), []byte("workspace\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, source, "add", "README.md")
	runGit(t, source, "commit", "-m", "initial")
	manager := &workspace.Manager{Root: t.TempDir()}
	prepared, err := manager.Prepare(context.Background(), "run-1", "attempt-1", relay.WorkspaceSpec{Kind: "git", Source: source})
	if err != nil {
		t.Fatal(err)
	}
	if content, err := os.ReadFile(filepath.Join(prepared.Dir, "README.md")); err != nil || string(content) != "workspace\n" {
		t.Fatalf("prepared content = %q, %v", content, err)
	}
	if err := prepared.Cleanup(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(prepared.Dir); !os.IsNotExist(err) {
		t.Fatalf("worktree still exists after cleanup: %v", err)
	}
}

func TestWorkspaceRejectsEscapingSubdir(t *testing.T) {
	manager := &workspace.Manager{Root: t.TempDir()}
	_, err := manager.Prepare(context.Background(), "run", "attempt", relay.WorkspaceSpec{Kind: "temp", Subdir: "../escape"})
	if err == nil {
		t.Fatal("escaping subdir was accepted")
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	command := exec.Command("git", args...)
	command.Dir = dir
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v: %s", args, err, output)
	}
}
