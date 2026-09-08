// Package trae adapts TraeCode CLI's codex-compatible exec protocol to Realy.
package trae

import (
	"context"
	"fmt"
	"os/exec"

	"github.com/KDF5000/realy"
	runtimecodex "github.com/KDF5000/realy/runtime/codex"
)

type Config = runtimecodex.Config
type ModelInfo = runtimecodex.ModelInfo
type Executor struct{ Config Config }

func (e Executor) Execute(ctx context.Context, execution realy.Execution) (realy.Result, error) {
	config := e.Config
	// Trae's interactive "default" mode may ask for approval, which is
	// impossible under `exec`. Omitting it selects Trae's headless default.
	if config.PermissionMode == "default" {
		config.PermissionMode = ""
	}
	if config.PermissionMode != "" && config.PermissionMode != "bypass_permissions" && config.PermissionMode != "custom" {
		return realy.Result{}, fmt.Errorf("realy trae: unsupported headless permission mode %q", config.PermissionMode)
	}
	if config.Binary == "" {
		config.Binary = ResolveBinary("")
	}
	return runtimecodex.ExecuteFork(ctx, config, execution, runtimecodex.ForkOptions{Name: "trae", DefaultBinary: "traex", InstructionProvider: "trae", PassEnv: []string{"TRAE_HOME", "TRAE_API_KEY", "TRAE_BASE_URL"}})
}

func ProbeVersion(ctx context.Context, binary string) (string, error) {
	binary = ResolveBinary(binary)
	output, err := exec.CommandContext(ctx, binary, "--version").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("realy trae: probe version: %w: %s", err, output)
	}
	return runtimecodex.ParseVersionOutput(string(output)), nil
}

func ProbeModels(ctx context.Context, config Config) ([]ModelInfo, error) {
	if config.Binary == "" {
		config.Binary = ResolveBinary("")
	}
	return runtimecodex.ProbeModels(ctx, config, runtimecodex.ForkOptions{Name: "trae", DefaultBinary: "traex", PassEnv: []string{"TRAE_HOME", "TRAE_API_KEY", "TRAE_BASE_URL"}})
}

func ResolveBinary(binary string) string {
	if binary != "" {
		return binary
	}
	if _, err := exec.LookPath("traex"); err == nil {
		return "traex"
	}
	return "trae-cli"
}
