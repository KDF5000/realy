package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/KDF5000/realy"
	"github.com/KDF5000/realy/binding"
	"github.com/KDF5000/realy/controlplane"
	"github.com/KDF5000/realy/node"
	runtimecodex "github.com/KDF5000/realy/runtime/codex"
	runtimecommand "github.com/KDF5000/realy/runtime/command"
	runtimetrae "github.com/KDF5000/realy/runtime/trae"
	"github.com/KDF5000/realy/transport/httpapi"
	"github.com/KDF5000/realy/workspace"
)

type config struct {
	Server        string                        `json:"server"`
	Token         string                        `json:"token,omitempty"`
	Node          controlplane.NodeRegistration `json:"node"`
	Runtimes      []runtimeConfig               `json:"runtimes"`
	Bindings      []bindingConfig               `json:"bindings"`
	WorkspaceRoot string                        `json:"workspace_root,omitempty"`
}
type runtimeConfig struct {
	ID               string                      `json:"id,omitempty"`
	Kind             string                      `json:"kind"`
	Provider         string                      `json:"provider"`
	Protocol         string                      `json:"protocol,omitempty"`
	Version          string                      `json:"version"`
	Command          string                      `json:"command"`
	ToolDir          string                      `json:"tool_dir,omitempty"`
	PassEnv          []string                    `json:"pass_env,omitempty"`
	Args             []string                    `json:"args,omitempty"`
	Env              map[string]string           `json:"env,omitempty"`
	InheritEnv       bool                        `json:"inherit_env"`
	Model            string                      `json:"model,omitempty"`
	Models           []string                    `json:"models,omitempty"`
	Profile          string                      `json:"profile,omitempty"`
	ReasoningEffort  string                      `json:"reasoning_effort,omitempty"`
	ServiceTier      string                      `json:"service_tier,omitempty"`
	Sandbox          string                      `json:"sandbox,omitempty"`
	WorkRoot         string                      `json:"work_root,omitempty"`
	Ephemeral        bool                        `json:"ephemeral,omitempty"`
	PermissionMode   string                      `json:"permission_mode,omitempty"`
	AllowedTools     []string                    `json:"allowed_tools,omitempty"`
	DisallowedTools  []string                    `json:"disallowed_tools,omitempty"`
	ShellToolTimeout string                      `json:"shell_tool_timeout,omitempty"`
	IgnoreUserConfig bool                        `json:"ignore_user_config,omitempty"`
	IgnoreRules      bool                        `json:"ignore_rules,omitempty"`
	DetectedModels   []controlplane.RuntimeModel `json:"-"`
	DetectedDefault  string                      `json:"-"`
}
type bindingConfig struct {
	Name       string            `json:"name"`
	Version    string            `json:"version"`
	Kind       string            `json:"kind"`
	Command    string            `json:"command,omitempty"`
	Args       []string          `json:"args,omitempty"`
	Endpoint   string            `json:"endpoint,omitempty"`
	TokenEnv   string            `json:"token_env,omitempty"`
	Env        map[string]string `json:"env,omitempty"`
	InheritEnv bool              `json:"inherit_env"`
}

func main() {
	configPath := flag.String("config", "realy-node.json", "node configuration file")
	poll := flag.Duration("poll", time.Second, "queue polling interval")
	heartbeat := flag.Duration("heartbeat", 5*time.Second, "node heartbeat interval")
	drainTimeout := flag.Duration("drain-timeout", 30*time.Second, "maximum graceful shutdown drain time")
	flag.Parse()
	if *poll <= 0 || *heartbeat <= 0 || *drainTimeout <= 0 {
		log.Fatal("poll, heartbeat, and drain intervals must be positive")
	}
	data, err := os.ReadFile(*configPath)
	if err != nil {
		log.Fatal(err)
	}
	var cfg config
	if err := json.Unmarshal(data, &cfg); err != nil {
		log.Fatal(err)
	}
	registry := binding.NewRegistry()
	for _, item := range cfg.Bindings {
		var provider realy.CapabilityProvider
		if item.Kind == "exec" {
			provider = binding.ExecProvider{Config: binding.Exec{Command: item.Command, Args: item.Args, Env: item.Env, InheritEnv: item.InheritEnv}}
		} else if item.Kind == "http" {
			provider = binding.HTTPProvider{Endpoint: item.Endpoint, Token: os.Getenv(item.TokenEnv)}
		} else {
			log.Fatalf("unsupported binding kind %q", item.Kind)
		}
		if err := registry.Register(binding.Descriptor{Name: item.Name, Version: item.Version, Kind: item.Kind}, provider); err != nil {
			log.Fatal(err)
		}
	}
	executors := node.ExecutorMap{}
	cfg.Node.Runtimes = nil
	for index := range cfg.Runtimes {
		item := &cfg.Runtimes[index]
		if item.ID == "" {
			item.ID = controlplane.RuntimeInstanceID(cfg.Node.ID, item.Provider)
		}
		if item.Kind == "codex" || (item.Kind == "" && item.Provider == "codex") {
			if item.Command == "" {
				item.Command = "codex"
			}
			if item.Version == "" {
				detected, probeErr := runtimecodex.ProbeVersion(context.Background(), item.Command)
				if probeErr == nil {
					item.Version = detected
				}
			}
			discoverRuntimeModels(item, "codex")
			executors[item.Provider] = runtimecodex.Executor{Config: compatibleConfig(*item)}
		} else if isTrae(*item) {
			if item.Command == "" {
				item.Command = runtimetrae.ResolveBinary("")
			}
			if item.Version == "" {
				if detected, probeErr := runtimetrae.ProbeVersion(context.Background(), item.Command); probeErr == nil {
					item.Version = detected
				}
			}
			discoverRuntimeModels(item, "trae")
			executors[item.Provider] = runtimetrae.Executor{Config: compatibleConfig(*item)}
		} else {
			executors[item.Provider] = runtimecommand.Executor{Command: item.Command, Args: item.Args, Env: item.Env, InheritEnv: item.InheritEnv}
		}
		cfg.Node.Runtimes = append(cfg.Node.Runtimes, probeRuntime(*item))
	}
	token := cfg.Token
	if token == "" {
		token = os.Getenv("REALY_NODE_TOKEN")
	}
	client := httpapi.NewAuthenticatedClient(cfg.Server, token)
	worker := &node.Worker{Registration: cfg.Node, ControlPlane: client, Bindings: registry, Executors: executors, Workspaces: &workspace.Manager{Root: cfg.WorkspaceRoot}}
	executionCtx, cancelExecutions := context.WithCancel(context.Background())
	defer cancelExecutions()
	claimCtx, stopClaims := context.WithCancel(context.Background())
	if _, err := worker.Register(executionCtx); err != nil {
		log.Fatal(err)
	}
	log.Printf("Realy node %s registered", cfg.Node.ID)
	go func() {
		ticker := time.NewTicker(*heartbeat)
		defer ticker.Stop()
		for {
			select {
			case <-executionCtx.Done():
				return
			case <-ticker.C:
				cfg.Node.Runtimes = nil
				for _, item := range cfg.Runtimes {
					cfg.Node.Runtimes = append(cfg.Node.Runtimes, probeRuntime(item))
				}
				worker.Registration.Runtimes = cfg.Node.Runtimes
				if _, err := worker.Register(executionCtx); err != nil && executionCtx.Err() == nil {
					log.Printf("heartbeat failed: %v", err)
				}
			}
		}
	}()
	poolDone := make(chan struct{})
	go func() {
		worker.RunPool(claimCtx, executionCtx, *poll, func(err error) {
			if !errors.Is(err, controlplane.ErrNoAssignment) {
				log.Printf("run failed: %v", err)
			}
		})
		close(poolDone)
	}()
	signals := make(chan os.Signal, 2)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	defer signal.Stop(signals)
	<-signals
	log.Printf("Realy node %s draining", cfg.Node.ID)
	stopClaims()
	timer := time.NewTimer(*drainTimeout)
	select {
	case <-poolDone:
		timer.Stop()
	case <-timer.C:
		log.Printf("drain timeout reached; terminating active runtimes")
		cancelExecutions()
		<-poolDone
	case <-signals:
		timer.Stop()
		log.Printf("second shutdown signal received; terminating active runtimes")
		cancelExecutions()
		<-poolDone
	}
	cancelExecutions()
}

func probeRuntime(item runtimeConfig) controlplane.Runtime {
	now := time.Now().UTC()
	defaultModel := item.Model
	if defaultModel == "" {
		defaultModel = item.DetectedDefault
	}
	result := controlplane.Runtime{ID: item.ID, Provider: item.Provider, Version: item.Version, State: "healthy", DefaultModel: defaultModel, Models: append([]string(nil), item.Models...), ModelCatalog: append([]controlplane.RuntimeModel(nil), item.DetectedModels...), CheckedAt: &now}
	for _, model := range item.DetectedModels {
		if !contains(result.Models, model.ID) {
			result.Models = append(result.Models, model.ID)
		}
	}
	for _, model := range item.Models {
		if !catalogContains(result.ModelCatalog, model) {
			result.ModelCatalog = append(result.ModelCatalog, controlplane.RuntimeModel{ID: model, DisplayName: model, Default: model == defaultModel})
		}
	}
	if item.Kind == "codex" || (item.Kind == "" && item.Provider == "codex") {
		command := item.Command
		if command == "" {
			command = "codex"
		}
		version, err := runtimecodex.ProbeVersion(context.Background(), command)
		if err != nil {
			result.State = "unhealthy"
			result.Message = err.Error()
		} else {
			result.Version = version
		}
		return result
	}
	if isTrae(item) {
		command := runtimetrae.ResolveBinary(item.Command)
		version, err := runtimetrae.ProbeVersion(context.Background(), command)
		if err != nil {
			result.State = "unhealthy"
			result.Message = err.Error()
		} else {
			result.Version = version
		}
		return result
	}
	if _, err := exec.LookPath(item.Command); err != nil {
		result.State = "unhealthy"
		result.Message = err.Error()
	}
	return result
}

func discoverRuntimeModels(item *runtimeConfig, kind string) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	var (
		models []runtimecodex.ModelInfo
		err    error
	)
	if kind == "trae" {
		models, err = runtimetrae.ProbeModels(ctx, compatibleConfig(*item))
	} else {
		models, err = runtimecodex.ProbeModels(ctx, compatibleConfig(*item), runtimecodex.ForkOptions{Name: "codex", DefaultBinary: "codex"})
	}
	if err != nil {
		log.Printf("Realy %s model discovery unavailable: %v", item.Provider, err)
		return
	}
	for _, model := range models {
		item.DetectedModels = append(item.DetectedModels, controlplane.RuntimeModel{ID: model.ID, DisplayName: model.DisplayName, Description: model.Description, Default: model.Default})
		if model.Default {
			item.DetectedDefault = model.ID
		}
	}
}

func contains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func catalogContains(values []controlplane.RuntimeModel, expected string) bool {
	for _, value := range values {
		if value.ID == expected {
			return true
		}
	}
	return false
}

func isTrae(item runtimeConfig) bool {
	return item.Kind == "trae" || item.Kind == "traex" || item.Kind == "trae-cli" || (item.Kind == "" && (item.Provider == "trae" || item.Provider == "traex"))
}
func compatibleConfig(item runtimeConfig) runtimecodex.Config {
	return runtimecodex.Config{Binary: item.Command, Protocol: item.Protocol, ToolDir: item.ToolDir, PassEnv: item.PassEnv, Env: item.Env, Model: item.Model, Profile: item.Profile, ReasoningEffort: item.ReasoningEffort, ServiceTier: item.ServiceTier, Sandbox: item.Sandbox, WorkRoot: item.WorkRoot, Ephemeral: item.Ephemeral, PermissionMode: item.PermissionMode, AllowedTools: item.AllowedTools, DisallowedTools: item.DisallowedTools, ShellToolTimeout: item.ShellToolTimeout, IgnoreUserConfig: item.IgnoreUserConfig, IgnoreRules: item.IgnoreRules, ExtraArgs: item.Args}
}
