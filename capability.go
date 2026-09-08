package realy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"
)

var (
	ErrCapabilityProviderMissing = errors.New("realy: capability provider is not configured")
	ErrCapabilityNotGranted      = errors.New("realy: capability not granted")
	ErrResourceOutOfScope        = errors.New("realy: capability resource out of scope")
	ErrIdempotencyConflict       = errors.New("realy: idempotency key reused for a different capability call")
)

type capabilityCallState struct {
	name     string
	version  string
	resource string
	done     chan struct{}
	result   CapabilityResult
	err      error
}

type scopedCapabilities struct {
	run       Run
	principal Principal
	grants    []CapabilityGrant
	provider  CapabilityProvider
	emit      func(context.Context, string, any)
	reserve   func(context.Context, string, string, CapabilityRequest) (CapabilityReservation, error)
	finish    func(context.Context, CapabilityReservation, CapabilityResult, string) error
	mu        sync.Mutex
	calls     map[string]*capabilityCallState
}

type CapabilityInvokerOptions struct {
	Run       Run
	Principal Principal
	Grants    []CapabilityGrant
	Provider  CapabilityProvider
	Emit      func(context.Context, string, any)
	Reserve   func(context.Context, string, string, CapabilityRequest) (CapabilityReservation, error)
	Finish    func(context.Context, CapabilityReservation, CapabilityResult, string) error
}

type CapabilityReservation struct {
	CallID  string            `json:"call_id"`
	Execute bool              `json:"execute"`
	Result  *CapabilityResult `json:"result,omitempty"`
	Error   string            `json:"error,omitempty"`
}

func NewCapabilityInvoker(options CapabilityInvokerOptions) CapabilityInvoker {
	emit := options.Emit
	if emit == nil {
		emit = func(context.Context, string, any) {}
	}
	return &scopedCapabilities{
		run:       options.Run,
		principal: options.Principal,
		grants:    append([]CapabilityGrant(nil), options.Grants...),
		provider:  options.Provider,
		emit:      emit,
		reserve:   options.Reserve,
		finish:    options.Finish,
		calls:     make(map[string]*capabilityCallState),
	}
}

func (s *scopedCapabilities) Call(ctx context.Context, call CapabilityCall) (CapabilityResult, error) {
	if s.provider == nil {
		return CapabilityResult{}, ErrCapabilityProviderMissing
	}
	if call.IdempotencyKey == "" {
		return CapabilityResult{}, errors.New("realy: capability idempotency key is required")
	}

	grant, err := findGrant(s.grants, call)
	if err != nil {
		return CapabilityResult{}, err
	}
	version := grant.Version
	s.mu.Lock()
	if previous, ok := s.calls[call.IdempotencyKey]; ok {
		if previous.name != call.Name || previous.version != version || previous.resource != call.Resource {
			s.mu.Unlock()
			return CapabilityResult{}, ErrIdempotencyConflict
		}
		done := previous.done
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			return CapabilityResult{}, ctx.Err()
		case <-done:
			return previous.result, previous.err
		}
	}
	state := &capabilityCallState{name: call.Name, version: version, resource: call.Resource, done: make(chan struct{})}
	s.calls[call.IdempotencyKey] = state
	s.mu.Unlock()

	request := CapabilityRequest{RunID: s.run.ID, AgentID: s.run.AgentID, Principal: s.principal, Name: grant.Name, Version: grant.Version, Effect: grant.Effect, Resource: call.Resource, Input: call.Input}
	reservation := CapabilityReservation{CallID: newID("call"), Execute: true}
	if s.reserve != nil {
		encoded, _ := json.Marshal(request)
		digest := sha256.Sum256(encoded)
		reservation, err = s.reserve(ctx, call.IdempotencyKey, hex.EncodeToString(digest[:]), request)
		if err != nil {
			s.finishCall(state, CapabilityResult{}, err)
			return CapabilityResult{}, err
		}
		if !reservation.Execute {
			if reservation.Error != "" {
				err = errors.New(reservation.Error)
				s.finishCall(state, CapabilityResult{}, err)
				return CapabilityResult{}, err
			}
			if reservation.Result == nil {
				err = errors.New("realy: capability call is still pending")
				s.finishCall(state, CapabilityResult{}, err)
				return CapabilityResult{}, err
			}
			s.finishCall(state, *reservation.Result, nil)
			return *reservation.Result, nil
		}
	}
	callID := reservation.CallID
	s.emit(ctx, "capability.started", map[string]string{"call_id": callID, "name": call.Name, "resource": call.Resource})
	request.CallID = callID
	output, err := s.provider.Invoke(ctx, request)
	if err != nil {
		if s.finish != nil {
			_ = s.finish(context.WithoutCancel(ctx), reservation, CapabilityResult{}, err.Error())
		}
		s.emit(ctx, "capability.failed", map[string]string{"call_id": callID, "name": call.Name, "error": err.Error()})
		s.finishCall(state, CapabilityResult{}, err)
		return CapabilityResult{}, err
	}
	result := CapabilityResult{CallID: callID, Output: output}
	if s.finish != nil {
		if err := s.finish(context.WithoutCancel(ctx), reservation, result, ""); err != nil {
			s.finishCall(state, CapabilityResult{}, err)
			return CapabilityResult{}, err
		}
	}
	s.finishCall(state, result, nil)
	s.emit(ctx, "capability.succeeded", map[string]string{"call_id": callID, "name": call.Name})
	return result, nil
}

func (s *scopedCapabilities) finishCall(state *capabilityCallState, result CapabilityResult, err error) {
	s.mu.Lock()
	state.result = result
	state.err = err
	close(state.done)
	s.mu.Unlock()
}

func findGrant(grants []CapabilityGrant, call CapabilityCall) (CapabilityGrant, error) {
	for _, grant := range grants {
		if grant.Name != call.Name || (call.Version != "" && grant.Version != call.Version) {
			continue
		}
		if len(grant.Resources) > 0 && !slices.Contains(grant.Resources, call.Resource) {
			return CapabilityGrant{}, fmt.Errorf("%w: %s", ErrResourceOutOfScope, call.Resource)
		}
		return grant, nil
	}
	return CapabilityGrant{}, fmt.Errorf("%w: %s", ErrCapabilityNotGranted, call.Name)
}
