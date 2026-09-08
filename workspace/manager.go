package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	"github.com/KDF5000/realy"
	runtimeprocess "github.com/KDF5000/realy/runtime/process"
)

type Prepared struct {
	Dir     string
	Cleanup func(context.Context) error
}

type Provider interface {
	Prepare(context.Context, string, string, realy.WorkspaceSpec) (Prepared, error)
}

// Manager prepares isolated workspaces and keeps bare Git mirrors for efficient
// reuse across attempts. It does not attach business semantics to repositories.
type Manager struct {
	Root string
	mu   sync.Mutex
}

func (m *Manager) Prepare(ctx context.Context, runID, attemptID string, spec realy.WorkspaceSpec) (Prepared, error) {
	root := m.Root
	if root == "" {
		root = filepath.Join(os.TempDir(), "realy-workspaces")
	}
	root, err := filepath.Abs(root)
	if err != nil {
		return Prepared{}, err
	}
	switch spec.Kind {
	case "", "temp":
		dir := filepath.Join(root, "runs", safeSegment(runID), safeSegment(attemptID))
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return Prepared{}, err
		}
		return preparedDirectory(root, dir, spec.Subdir, spec.Ephemeral || spec.Kind == "temp")
	case "local":
		if spec.Source == "" || !filepath.IsAbs(spec.Source) {
			return Prepared{}, errors.New("realy workspace: local source must be an absolute path")
		}
		info, err := os.Stat(spec.Source)
		if err != nil || !info.IsDir() {
			return Prepared{}, fmt.Errorf("realy workspace: local source is not a directory: %s", spec.Source)
		}
		return preparedDirectory("", spec.Source, spec.Subdir, false)
	case "git":
		if spec.Source == "" {
			return Prepared{}, errors.New("realy workspace: git source is required")
		}
		return m.prepareGit(ctx, root, runID, attemptID, spec)
	default:
		return Prepared{}, fmt.Errorf("realy workspace: unsupported kind %q", spec.Kind)
	}
}

func (m *Manager) prepareGit(ctx context.Context, root, runID, attemptID string, spec realy.WorkspaceSpec) (Prepared, error) {
	digest := sha256.Sum256([]byte(spec.Source))
	mirror := filepath.Join(root, "git", hex.EncodeToString(digest[:12])+".git")
	worktree := filepath.Join(root, "runs", safeSegment(runID), safeSegment(attemptID))
	ref := spec.Ref
	if ref == "" {
		ref = "HEAD"
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, err := os.Stat(mirror); errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(mirror), 0o700); err != nil {
			return Prepared{}, err
		}
		if err := runGit(ctx, "", "clone", "--mirror", "--", spec.Source, mirror); err != nil {
			return Prepared{}, err
		}
	} else if err != nil {
		return Prepared{}, err
	} else if err := runGit(ctx, mirror, "fetch", "--prune", "origin"); err != nil {
		return Prepared{}, err
	}
	if err := os.MkdirAll(filepath.Dir(worktree), 0o700); err != nil {
		return Prepared{}, err
	}
	_ = os.RemoveAll(worktree)
	if err := runGit(ctx, mirror, "worktree", "add", "--detach", worktree, ref); err != nil {
		return Prepared{}, err
	}
	prepared, err := preparedDirectory(root, worktree, spec.Subdir, false)
	if err != nil {
		_ = runGit(context.Background(), mirror, "worktree", "remove", "--force", worktree)
		return Prepared{}, err
	}
	prepared.Cleanup = func(cleanupCtx context.Context) error {
		m.mu.Lock()
		defer m.mu.Unlock()
		return runGit(cleanupCtx, mirror, "worktree", "remove", "--force", worktree)
	}
	return prepared, nil
}

func preparedDirectory(root, dir, subdir string, ephemeral bool) (Prepared, error) {
	target := filepath.Join(dir, filepath.Clean(subdir))
	if subdir == "" {
		target = dir
	}
	target, err := filepath.Abs(target)
	if err != nil {
		return Prepared{}, err
	}
	base, err := filepath.Abs(dir)
	if err != nil {
		return Prepared{}, err
	}
	if target != base && !strings.HasPrefix(target, base+string(os.PathSeparator)) {
		return Prepared{}, errors.New("realy workspace: subdir escapes workspace")
	}
	info, err := os.Stat(target)
	if err != nil || !info.IsDir() {
		return Prepared{}, fmt.Errorf("realy workspace: subdir is not a directory: %s", subdir)
	}
	cleanup := func(context.Context) error { return nil }
	if ephemeral && root != "" {
		cleanup = func(context.Context) error { return os.RemoveAll(base) }
	}
	return Prepared{Dir: target, Cleanup: cleanup}, nil
}

func runGit(ctx context.Context, dir string, args ...string) error {
	command := exec.CommandContext(ctx, "git", args...)
	runtimeprocess.Configure(command)
	command.Dir = dir
	output, err := command.CombinedOutput()
	if err != nil {
		return fmt.Errorf("realy workspace: git %s: %w: %s", args[0], err, strings.TrimSpace(string(output)))
	}
	return nil
}

func safeSegment(value string) string {
	value = strings.ReplaceAll(value, string(os.PathSeparator), "_")
	value = strings.ReplaceAll(value, "..", "_")
	return value
}
