package node

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"github.com/KDF5000/realy"
	"github.com/KDF5000/realy/binding"
	"github.com/KDF5000/realy/controlplane"
	"github.com/KDF5000/realy/workspace"
)

var ErrLeaseLost = errors.New("realy node: assignment lease lost")

type ControlPlane interface {
	RegisterNode(context.Context, controlplane.NodeRegistration) (controlplane.Node, error)
	Heartbeat(context.Context, string) (controlplane.Node, error)
	Claim(context.Context, string) (controlplane.Assignment, error)
	Start(context.Context, controlplane.Assignment) error
	Renew(context.Context, controlplane.Assignment) (controlplane.LeaseUpdate, error)
	AppendEvent(context.Context, string, string, string, string, any) error
	Complete(context.Context, controlplane.Assignment, realy.Result) error
	Fail(context.Context, controlplane.Assignment, string) error
	AcknowledgeCancellation(context.Context, controlplane.Assignment) error
	UploadArtifact(context.Context, controlplane.Assignment, realy.Artifact, io.Reader) (realy.Artifact, error)
	ReserveCapability(context.Context, controlplane.Assignment, string, string, realy.CapabilityRequest) (realy.CapabilityReservation, error)
	FinishCapability(context.Context, controlplane.Assignment, realy.CapabilityReservation, realy.CapabilityResult, string) error
	CreateInteraction(context.Context, controlplane.Assignment, realy.InteractionRequest) (realy.Interaction, error)
	GetInteraction(context.Context, controlplane.Assignment, string) (realy.Interaction, error)
}

type ExecutorResolver interface {
	Resolve(provider string) (realy.Executor, bool)
}

type ExecutorMap map[string]realy.Executor

func (m ExecutorMap) Resolve(provider string) (realy.Executor, bool) {
	executor, ok := m[provider]
	return executor, ok
}

type Worker struct {
	Registration controlplane.NodeRegistration
	ControlPlane ControlPlane
	Bindings     *binding.Registry
	Executors    ExecutorResolver
	Compiler     realy.InstructionCompiler
	Workspaces   workspace.Provider
}

// RunPool runs one claim loop per configured capacity slot. Cancelling
// claimCtx stops new assignments while executionCtx remains alive so callers
// can drain in-flight runtimes before forcing shutdown.
func (w *Worker) RunPool(claimCtx, executionCtx context.Context, poll time.Duration, onError func(error)) {
	capacity := w.Registration.Capacity
	if capacity <= 0 {
		capacity = 1
	}
	if poll <= 0 {
		poll = time.Second
	}
	if onError == nil {
		onError = func(error) {}
	}
	var workers sync.WaitGroup
	workers.Add(capacity)
	for range capacity {
		go func() {
			defer workers.Done()
			for {
				select {
				case <-claimCtx.Done():
					return
				default:
				}
				_, err := w.RunOnce(executionCtx)
				if err != nil && !errors.Is(err, controlplane.ErrNoAssignment) && executionCtx.Err() == nil {
					onError(err)
				}
				timer := time.NewTimer(poll)
				select {
				case <-claimCtx.Done():
					timer.Stop()
					return
				case <-executionCtx.Done():
					timer.Stop()
					return
				case <-timer.C:
				}
			}
		}()
	}
	workers.Wait()
}

func (w *Worker) Register(ctx context.Context) (controlplane.Node, error) {
	if w.ControlPlane == nil {
		return controlplane.Node{}, errors.New("realy node: control plane is required")
	}
	if w.Bindings != nil {
		w.Registration.Capabilities = nil
		for _, descriptor := range w.Bindings.Inventory() {
			w.Registration.Capabilities = append(w.Registration.Capabilities, controlplane.Capability{Name: descriptor.Name, Version: descriptor.Version, Kind: descriptor.Kind})
		}
	}
	return w.ControlPlane.RegisterNode(ctx, w.Registration)
}

func (w *Worker) RunOnce(ctx context.Context) (realy.Run, error) {
	if w.ControlPlane == nil || w.Executors == nil {
		return realy.Run{}, errors.New("realy node: control plane and executors are required")
	}
	assignment, err := w.ControlPlane.Claim(ctx, w.Registration.ID)
	if err != nil {
		return realy.Run{}, err
	}
	executor, ok := w.Executors.Resolve(assignment.Request.Runtime.Provider)
	if !ok {
		cause := fmt.Sprintf("runtime provider %q is unavailable", assignment.Request.Runtime.Provider)
		_ = w.ControlPlane.Fail(ctx, assignment, cause)
		return realy.Run{}, errors.New(cause)
	}
	workDir := ""
	cleanupWorkspace := func(context.Context) error { return nil }
	if assignment.Request.Workspace.Kind != "" {
		if w.Workspaces == nil {
			cause := "workspace requested but no workspace provider is configured"
			_ = w.ControlPlane.Fail(ctx, assignment, cause)
			return realy.Run{}, errors.New(cause)
		}
		prepared, prepareErr := w.Workspaces.Prepare(ctx, assignment.RunID, assignment.AttemptID, assignment.Request.Workspace)
		if prepareErr != nil {
			_ = w.ControlPlane.Fail(ctx, assignment, prepareErr.Error())
			return realy.Run{}, prepareErr
		}
		workDir, cleanupWorkspace = prepared.Dir, prepared.Cleanup
		defer func() {
			cleanupCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			_ = cleanupWorkspace(cleanupCtx)
		}()
	}
	compiler := w.Compiler
	if compiler == nil {
		compiler = realy.DefaultInstructionCompiler{}
	}
	instructions := assignment.Request.Instructions
	if len(assignment.Request.Capabilities) > 0 {
		instructions.Runtime = append(append([]realy.InstructionFragment(nil), instructions.Runtime...), realy.CapabilityToolInstruction(assignment.Request.Capabilities))
	}
	compiled, err := compiler.Compile(assignment.Request.Input, instructions)
	if err != nil {
		_ = w.ControlPlane.Fail(ctx, assignment, err.Error())
		return realy.Run{}, err
	}
	if err := w.ControlPlane.Start(ctx, assignment); err != nil {
		if errors.Is(err, controlplane.ErrRunCancelled) {
			if ackErr := w.ControlPlane.AcknowledgeCancellation(ctx, assignment); ackErr != nil {
				return realy.Run{}, ackErr
			}
			return cancelledRun(assignment), nil
		}
		return realy.Run{}, err
	}
	executionCtx, cancelExecution := context.WithCancel(ctx)
	defer cancelExecution()
	keeperCtx, stopKeeper := context.WithCancel(ctx)
	keeperDone := make(chan error, 1)
	go func() {
		keeperDone <- w.keepLease(keeperCtx, assignment, cancelExecution)
	}()
	run := realy.Run{ID: assignment.RunID, AgentID: assignment.Request.AgentID, Runtime: assignment.Request.Runtime, Source: assignment.Request.Source, Attempt: realy.Attempt{ID: assignment.AttemptID, NodeID: w.Registration.ID}}
	provider := realy.CapabilityProvider(nil)
	if w.Bindings != nil {
		provider = w.Bindings
	}
	invoker := realy.NewCapabilityInvoker(realy.CapabilityInvokerOptions{
		Run: run, Principal: assignment.Request.Principal, Grants: assignment.Request.Capabilities, Provider: provider,
		Emit: func(eventCtx context.Context, eventType string, data any) {
			_ = w.ControlPlane.AppendEvent(eventCtx, assignment.RunID, assignment.AttemptID, assignment.LeaseToken, eventType, data)
		},
		Reserve: func(callCtx context.Context, key, hash string, request realy.CapabilityRequest) (realy.CapabilityReservation, error) {
			return w.ControlPlane.ReserveCapability(callCtx, assignment, key, hash, request)
		},
		Finish: func(callCtx context.Context, reservation realy.CapabilityReservation, result realy.CapabilityResult, cause string) error {
			return w.ControlPlane.FinishCapability(callCtx, assignment, reservation, result, cause)
		},
	})
	result, executionErr := executor.Execute(executionCtx, realy.Execution{
		RunID: assignment.RunID, AttemptID: assignment.AttemptID, AgentID: assignment.Request.AgentID,
		Runtime: assignment.Request.Runtime,
		Source:  assignment.Request.Source, Input: assignment.Request.Input, Context: assignment.Request.Context,
		Instructions: compiled, Capabilities: invoker,
		WorkDir:      workDir,
		Interactions: interactionBroker{controlPlane: w.ControlPlane, assignment: assignment},
		Emit: func(eventCtx context.Context, eventType string, data any) {
			_ = w.ControlPlane.AppendEvent(eventCtx, assignment.RunID, assignment.AttemptID, assignment.LeaseToken, eventType, data)
		},
	})
	stopKeeper()
	leaseErr := <-keeperDone
	if leaseErr != nil {
		if errors.Is(leaseErr, controlplane.ErrRunCancelled) {
			if err := w.ControlPlane.AcknowledgeCancellation(ctx, assignment); err != nil {
				return realy.Run{}, err
			}
			return cancelledRun(assignment), nil
		}
		return realy.Run{}, leaseErr
	}
	if executionErr != nil {
		_ = w.ControlPlane.Fail(ctx, assignment, executionErr.Error())
		return realy.Run{}, executionErr
	}
	for index, artifact := range result.Artifacts {
		if artifact.Ref == "" {
			continue
		}
		file, openErr := os.Open(artifact.Ref)
		if openErr != nil {
			_ = w.ControlPlane.Fail(ctx, assignment, openErr.Error())
			return realy.Run{}, openErr
		}
		uploaded, uploadErr := w.ControlPlane.UploadArtifact(ctx, assignment, artifact, file)
		_ = file.Close()
		if uploadErr != nil {
			_ = w.ControlPlane.Fail(ctx, assignment, uploadErr.Error())
			return realy.Run{}, uploadErr
		}
		result.Artifacts[index] = uploaded
	}
	if err := w.ControlPlane.Complete(ctx, assignment, result); err != nil {
		return realy.Run{}, err
	}
	return realy.Run{ID: assignment.RunID, AgentID: assignment.Request.AgentID, Runtime: assignment.Request.Runtime, Source: assignment.Request.Source, Status: realy.RunSucceeded, Result: &result}, nil
}

type interactionBroker struct {
	controlPlane ControlPlane
	assignment   controlplane.Assignment
}

func (b interactionBroker) Request(ctx context.Context, request realy.InteractionRequest) (json.RawMessage, error) {
	value, err := b.controlPlane.CreateInteraction(ctx, b.assignment, request)
	if err != nil {
		return nil, err
	}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-ticker.C:
			value, err = b.controlPlane.GetInteraction(ctx, b.assignment, value.ID)
			if err != nil {
				return nil, err
			}
			if value.State == "resolved" {
				return value.Response, nil
			}
		}
	}
}

func (w *Worker) keepLease(ctx context.Context, assignment controlplane.Assignment, cancelExecution context.CancelFunc) error {
	deadline := assignment.LeaseExpiresAt
	for {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			cancelExecution()
			return ErrLeaseLost
		}
		wait := remaining / 3
		if wait > 5*time.Second {
			wait = 5 * time.Second
		}
		if wait < 10*time.Millisecond {
			wait = 10 * time.Millisecond
		}
		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil
		case <-timer.C:
		}
		renewCtx, cancel := context.WithDeadline(ctx, deadline)
		update, err := w.ControlPlane.Renew(renewCtx, assignment)
		cancel()
		if err == nil {
			deadline = update.LeaseExpiresAt
			if update.CancelRequested {
				cancelExecution()
				return fmt.Errorf("%w: %s", controlplane.ErrRunCancelled, update.CancelReason)
			}
			continue
		}
		if ctx.Err() != nil {
			return nil
		}
		if errors.Is(err, controlplane.ErrInvalidLease) || errors.Is(err, controlplane.ErrInvalidTransition) || errors.Is(err, controlplane.ErrNotFound) {
			cancelExecution()
			return fmt.Errorf("%w: %v", ErrLeaseLost, err)
		}
		if time.Now().Before(deadline) {
			continue
		}
		cancelExecution()
		return fmt.Errorf("%w: %v", ErrLeaseLost, err)
	}
}

func cancelledRun(assignment controlplane.Assignment) realy.Run {
	return realy.Run{
		ID: assignment.RunID, AgentID: assignment.Request.AgentID, Runtime: assignment.Request.Runtime,
		Source: assignment.Request.Source, Status: realy.RunCancelled,
		Attempt: realy.Attempt{ID: assignment.AttemptID, Status: realy.AttemptCancelled},
	}
}
