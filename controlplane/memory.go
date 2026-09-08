package controlplane

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/KDF5000/realy"
)

// MemoryStorage is intended for tests and demos. PostgreSQL is the production
// control-plane storage implementation.
type MemoryStorage struct {
	mu              sync.Mutex
	nodes           map[string]*Node
	runs            map[string]*realy.Run
	requests        map[string]realy.Request
	events          map[string][]realy.Event
	byIdempotency   map[string]string
	artifacts       map[string]realy.Artifact
	artifactRuns    map[string][]string
	capabilityCalls map[string]memoryCapabilityCall
	attemptHistory  map[string][]realy.Attempt
	interactions    map[string]*realy.Interaction
}

type memoryCapabilityCall struct {
	hash        string
	reservation realy.CapabilityReservation
}

func NewMemoryStorage() *MemoryStorage {
	return &MemoryStorage{nodes: make(map[string]*Node), runs: make(map[string]*realy.Run), requests: make(map[string]realy.Request), events: make(map[string][]realy.Event), byIdempotency: make(map[string]string), artifacts: make(map[string]realy.Artifact), artifactRuns: make(map[string][]string), capabilityCalls: make(map[string]memoryCapabilityCall), attemptHistory: make(map[string][]realy.Attempt), interactions: make(map[string]*realy.Interaction)}
}

func (s *MemoryStorage) RegisterNode(_ context.Context, registration NodeRegistration) (Node, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	if existing := s.nodes[registration.ID]; existing != nil {
		existing.NodeRegistration = registration
		existing.LastSeen = now
		return *existing, nil
	}
	node := &Node{NodeRegistration: registration, LastSeen: now}
	s.nodes[registration.ID] = node
	return *node, nil
}

func (s *MemoryStorage) Heartbeat(_ context.Context, nodeID string) (Node, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	node := s.nodes[nodeID]
	if node == nil {
		return Node{}, ErrNotFound
	}
	node.LastSeen = time.Now().UTC()
	return *node, nil
}

func (s *MemoryStorage) Submit(_ context.Context, request realy.Request) (realy.Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	idempotencyScope := request.TenantID + "\x00" + request.ProjectID + "\x00" + request.IdempotencyKey
	if runID := s.byIdempotency[idempotencyScope]; runID != "" {
		return *s.runs[runID], nil
	}
	now := time.Now().UTC()
	run := &realy.Run{ID: newControlPlaneID("run"), TenantID: request.TenantID, ProjectID: request.ProjectID, SessionID: request.SessionID, AgentID: request.AgentID, IdempotencyKey: request.IdempotencyKey, Runtime: request.Runtime, Source: request.Source, Status: realy.RunQueued, CreatedAt: now, Attempt: realy.Attempt{ID: newControlPlaneID("attempt"), Number: 1, Status: realy.AttemptQueued, AvailableAt: &now}}
	if timeout, _ := time.ParseDuration(request.Timeout); timeout > 0 {
		deadline := now.Add(timeout)
		run.DeadlineAt = &deadline
	}
	s.runs[run.ID] = run
	s.attemptHistory[run.ID] = []realy.Attempt{run.Attempt}
	s.requests[run.ID] = request
	s.byIdempotency[idempotencyScope] = run.ID
	s.appendEvent(run, "run.created", map[string]any{"status": run.Status})
	s.appendEvent(run, "attempt.queued", map[string]any{"number": 1})
	return *run, nil
}

func (s *MemoryStorage) Claim(_ context.Context, nodeID string, leaseTTL time.Duration) (Assignment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	node := s.nodes[nodeID]
	if node == nil {
		return Assignment{}, ErrNotFound
	}
	if node.Active >= node.Capacity {
		return Assignment{}, ErrNoAssignment
	}
	now := time.Now().UTC()
	for _, run := range s.runs {
		if run.DeadlineAt != nil && !now.Before(*run.DeadlineAt) && run.CancelRequestedAt == nil {
			s.cancelRunLocked(run, CancelRequest{Reason: "run timeout exceeded", RequestedBy: "realy"}, now)
		}
		if run.Status == realy.RunCancelling && (run.Attempt.Status == realy.AttemptLeased || run.Attempt.Status == realy.AttemptRunning) && run.Attempt.LeaseExpiresAt != nil && !now.Before(*run.Attempt.LeaseExpiresAt) {
			s.finalizeCancellation(run, now)
			continue
		}
		s.requeueExpired(run, now)
	}
	for _, run := range s.runs {
		if run.Status != realy.RunQueued || run.Attempt.Status != realy.AttemptQueued || (run.Attempt.AvailableAt != nil && now.Before(*run.Attempt.AvailableAt)) {
			continue
		}
		request := s.requests[run.ID]
		if !nodeMatches(*node, request) {
			continue
		}
		run.Attempt.Status = realy.AttemptLeased
		run.Attempt.NodeID = nodeID
		run.Attempt.LeaseToken = newControlPlaneID("lease")
		node.Active++
		expires := now.Add(leaseTTL)
		run.Attempt.LeaseExpiresAt = &expires
		s.appendEvent(run, "attempt.leased", map[string]any{"node_id": nodeID, "lease_expires_at": expires})
		return Assignment{RunID: run.ID, AttemptID: run.Attempt.ID, LeaseToken: run.Attempt.LeaseToken, LeaseExpiresAt: expires, Request: request}, nil
	}
	return Assignment{}, ErrNoAssignment
}

func (s *MemoryStorage) Renew(_ context.Context, assignment Assignment, leaseTTL time.Duration) (LeaseUpdate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	run, err := s.authorize(assignment.RunID, assignment.AttemptID, assignment.LeaseToken)
	if err != nil {
		return LeaseUpdate{}, err
	}
	if run.Attempt.Status != realy.AttemptLeased && run.Attempt.Status != realy.AttemptRunning {
		return LeaseUpdate{}, ErrInvalidTransition
	}
	now := time.Now().UTC()
	if run.Attempt.LeaseExpiresAt == nil || !now.Before(*run.Attempt.LeaseExpiresAt) {
		return LeaseUpdate{}, ErrInvalidLease
	}
	expires := now.Add(leaseTTL)
	run.Attempt.LeaseExpiresAt = &expires
	return LeaseUpdate{LeaseExpiresAt: expires, CancelRequested: run.CancelRequestedAt != nil, CancelReason: run.CancelReason}, nil
}

func (s *MemoryStorage) Reconcile(_ context.Context, now time.Time, limit int) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	recovered := 0
	for _, run := range s.runs {
		if recovered >= limit {
			break
		}
		if run.DeadlineAt != nil && !now.Before(*run.DeadlineAt) && !terminalRun(run.Status) && run.CancelRequestedAt == nil {
			s.cancelRunLocked(run, CancelRequest{Reason: "run timeout exceeded", RequestedBy: "realy"}, now)
			recovered++
		}
	}
	for _, run := range s.runs {
		activeAttempt := run.Attempt.Status == realy.AttemptRunning || (run.Status == realy.RunCancelling && run.Attempt.Status == realy.AttemptLeased)
		if recovered >= limit || (run.Status != realy.RunRunning && run.Status != realy.RunCancelling) || !activeAttempt || run.Attempt.LeaseExpiresAt == nil || now.Before(*run.Attempt.LeaseExpiresAt) {
			continue
		}
		if run.Status == realy.RunCancelling {
			s.finalizeCancellation(run, now)
			recovered++
			continue
		}
		s.releaseNode(run.Attempt.NodeID)
		run.Attempt.Status = realy.AttemptLost
		run.Attempt.CompletedAt = timePointer(now)
		s.appendEvent(run, "attempt.lost", map[string]any{"node_id": run.Attempt.NodeID, "reason": "lease_expired"})
		request := s.requests[run.ID]
		if run.Attempt.Number >= request.Retry.MaxAttempts {
			run.Status, run.Error, run.CompletedAt = realy.RunFailed, "attempt lease expired", timePointer(now)
			s.appendEvent(run, "run.failed", map[string]string{"error": run.Error})
		} else {
			next := now.Add(retryBackoff(request.Retry))
			history := s.attemptHistory[run.ID]
			history[len(history)-1] = run.Attempt
			run.Attempt = realy.Attempt{ID: newControlPlaneID("attempt"), Number: run.Attempt.Number + 1, Status: realy.AttemptQueued, AvailableAt: &next}
			s.attemptHistory[run.ID] = append(history, run.Attempt)
			run.Status, run.Error, run.StartedAt, run.CompletedAt = realy.RunQueued, "", nil, nil
			s.appendEvent(run, "attempt.queued", map[string]any{"number": run.Attempt.Number, "available_at": next, "reason": "retry"})
		}
		recovered++
	}
	return recovered, nil
}

func (s *MemoryStorage) CancelRun(_ context.Context, runID string, request CancelRequest) (realy.Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	run := s.runs[runID]
	if run == nil {
		return realy.Run{}, ErrNotFound
	}
	s.cancelRunLocked(run, request, time.Now().UTC())
	return *run, nil
}

func (s *MemoryStorage) AcknowledgeCancellation(_ context.Context, assignment Assignment) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	run, err := s.authorize(assignment.RunID, assignment.AttemptID, assignment.LeaseToken)
	if err != nil {
		return err
	}
	if run.Status == realy.RunCancelled {
		return nil
	}
	if run.Status != realy.RunCancelling {
		return ErrInvalidTransition
	}
	s.finalizeCancellation(run, time.Now().UTC())
	return nil
}

func (s *MemoryStorage) Start(_ context.Context, assignment Assignment) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	run, err := s.authorize(assignment.RunID, assignment.AttemptID, assignment.LeaseToken)
	if err != nil {
		return err
	}
	if run.Attempt.Status != realy.AttemptLeased {
		return ErrInvalidTransition
	}
	if run.Status == realy.RunCancelling {
		s.finalizeCancellation(run, time.Now().UTC())
		return ErrRunCancelled
	}
	if run.Attempt.LeaseExpiresAt != nil && !time.Now().UTC().Before(*run.Attempt.LeaseExpiresAt) {
		s.requeueExpired(run, time.Now().UTC())
		return ErrInvalidLease
	}
	now := time.Now().UTC()
	run.Status, run.StartedAt = realy.RunRunning, &now
	run.Attempt.Status, run.Attempt.StartedAt = realy.AttemptRunning, &now
	s.appendEvent(run, "attempt.started", map[string]string{"node_id": run.Attempt.NodeID})
	s.appendEvent(run, "run.started", nil)
	return nil
}

func (s *MemoryStorage) AppendEvent(_ context.Context, runID, attemptID, lease, eventType string, data any) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	run, err := s.authorize(runID, attemptID, lease)
	if err != nil {
		return err
	}
	if run.Attempt.Status != realy.AttemptRunning {
		return ErrInvalidTransition
	}
	s.appendEvent(run, eventType, data)
	return nil
}

func (s *MemoryStorage) Complete(_ context.Context, assignment Assignment, result realy.Result) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	run, err := s.authorize(assignment.RunID, assignment.AttemptID, assignment.LeaseToken)
	if err != nil {
		return err
	}
	if run.Attempt.Status != realy.AttemptRunning {
		return ErrInvalidTransition
	}
	if run.Status == realy.RunCancelling {
		return ErrRunCancelled
	}
	now := time.Now().UTC()
	run.Status, run.Result, run.CompletedAt = realy.RunSucceeded, &result, &now
	run.Attempt.Status, run.Attempt.CompletedAt = realy.AttemptSucceeded, &now
	s.releaseNode(run.Attempt.NodeID)
	s.appendEvent(run, "attempt.succeeded", nil)
	s.appendEvent(run, "run.succeeded", result)
	return nil
}

func (s *MemoryStorage) Fail(_ context.Context, assignment Assignment, cause string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	run, err := s.authorize(assignment.RunID, assignment.AttemptID, assignment.LeaseToken)
	if err != nil {
		return err
	}
	if run.Attempt.Status != realy.AttemptRunning && run.Attempt.Status != realy.AttemptLeased {
		return ErrInvalidTransition
	}
	if run.Status == realy.RunCancelling {
		s.finalizeCancellation(run, time.Now().UTC())
		return ErrRunCancelled
	}
	now := time.Now().UTC()
	run.Status, run.Error, run.CompletedAt = realy.RunFailed, cause, &now
	run.Attempt.Status, run.Attempt.CompletedAt = realy.AttemptFailed, &now
	s.releaseNode(run.Attempt.NodeID)
	s.appendEvent(run, "attempt.failed", map[string]string{"error": cause})
	s.appendEvent(run, "run.failed", map[string]string{"error": cause})
	return nil
}

func (s *MemoryStorage) GetRun(_ context.Context, runID string) (realy.Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if run := s.runs[runID]; run != nil {
		return *run, nil
	}
	return realy.Run{}, ErrNotFound
}

func (s *MemoryStorage) Events(ctx context.Context, runID string) ([]realy.Event, error) {
	return s.EventsAfter(ctx, runID, 0)
}

func (s *MemoryStorage) EventsAfter(_ context.Context, runID string, after int) ([]realy.Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.runs[runID] == nil {
		return nil, ErrNotFound
	}
	values := s.events[runID]
	index := 0
	for index < len(values) && values[index].Sequence <= after {
		index++
	}
	return append([]realy.Event(nil), values[index:]...), nil
}

func (s *MemoryStorage) Nodes(_ context.Context) ([]Node, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := make([]Node, 0, len(s.nodes))
	for _, node := range s.nodes {
		result = append(result, *node)
	}
	return result, nil
}

func (s *MemoryStorage) AddArtifact(_ context.Context, assignment Assignment, artifact realy.Artifact) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	run, err := s.authorize(assignment.RunID, assignment.AttemptID, assignment.LeaseToken)
	if err != nil {
		return err
	}
	if run.Attempt.Status != realy.AttemptRunning {
		return ErrInvalidTransition
	}
	s.artifacts[artifact.ID] = artifact
	s.artifactRuns[run.ID] = append(s.artifactRuns[run.ID], artifact.ID)
	s.appendEvent(run, "artifact.created", artifact)
	return nil
}

func (s *MemoryStorage) Artifacts(_ context.Context, runID string) ([]realy.Artifact, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.runs[runID] == nil {
		return nil, ErrNotFound
	}
	result := make([]realy.Artifact, 0, len(s.artifactRuns[runID]))
	for _, id := range s.artifactRuns[runID] {
		result = append(result, s.artifacts[id])
	}
	return result, nil
}

func (s *MemoryStorage) Artifact(_ context.Context, artifactID string) (realy.Artifact, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.artifacts[artifactID]
	if !ok {
		return realy.Artifact{}, ErrNotFound
	}
	return value, nil
}

func (s *MemoryStorage) ReserveCapability(_ context.Context, a Assignment, key, hash string, _ realy.CapabilityRequest) (realy.CapabilityReservation, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	run, err := s.authorize(a.RunID, a.AttemptID, a.LeaseToken)
	if err != nil {
		return realy.CapabilityReservation{}, err
	}
	if run.Attempt.Status != realy.AttemptRunning {
		return realy.CapabilityReservation{}, ErrInvalidTransition
	}
	mapKey := a.RunID + "\x00" + key
	if old, ok := s.capabilityCalls[mapKey]; ok {
		if old.hash != hash {
			return realy.CapabilityReservation{}, realy.ErrIdempotencyConflict
		}
		return old.reservation, nil
	}
	reservation := realy.CapabilityReservation{CallID: newControlPlaneID("call"), Execute: true}
	s.capabilityCalls[mapKey] = memoryCapabilityCall{hash: hash, reservation: reservation}
	return reservation, nil
}

func (s *MemoryStorage) FinishCapability(_ context.Context, a Assignment, reservation realy.CapabilityReservation, result realy.CapabilityResult, cause string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	run, err := s.authorize(a.RunID, a.AttemptID, a.LeaseToken)
	if err != nil {
		return err
	}
	for key, value := range s.capabilityCalls {
		if value.reservation.CallID != reservation.CallID {
			continue
		}
		value.reservation.Execute = false
		value.reservation.Result = &result
		value.reservation.Error = cause
		s.capabilityCalls[key] = value
		s.appendEvent(run, "capability.recorded", map[string]string{"call_id": reservation.CallID})
		return nil
	}
	return ErrNotFound
}

func (s *MemoryStorage) ListRuns(_ context.Context, tenant, project, session string, limit int) ([]realy.Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var values []realy.Run
	for _, run := range s.runs {
		if (tenant == "" || run.TenantID == tenant) && (project == "" || run.ProjectID == project) && (session == "" || run.SessionID == session) {
			values = append(values, *run)
		}
	}
	sort.Slice(values, func(i, j int) bool { return values[i].CreatedAt.After(values[j].CreatedAt) })
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if len(values) > limit {
		values = values[:limit]
	}
	return values, nil
}
func (s *MemoryStorage) Attempts(_ context.Context, runID string) ([]realy.Attempt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	run := s.runs[runID]
	if run == nil {
		return nil, ErrNotFound
	}
	values := append([]realy.Attempt(nil), s.attemptHistory[runID]...)
	if len(values) > 0 {
		values[len(values)-1] = run.Attempt
	}
	return values, nil
}
func (s *MemoryStorage) CreateInteraction(_ context.Context, a Assignment, request realy.InteractionRequest) (realy.Interaction, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	run, err := s.authorize(a.RunID, a.AttemptID, a.LeaseToken)
	if err != nil {
		return realy.Interaction{}, err
	}
	if run.Attempt.Status != realy.AttemptRunning {
		return realy.Interaction{}, ErrInvalidTransition
	}
	value := realy.Interaction{ID: newControlPlaneID("interaction"), RunID: run.ID, AttemptID: run.Attempt.ID, Kind: request.Kind, State: "pending", Prompt: request.Prompt, Data: request.Data, CreatedAt: time.Now().UTC()}
	s.interactions[value.ID] = &value
	s.appendEvent(run, "interaction.requested", value)
	return value, nil
}
func (s *MemoryStorage) GetInteraction(_ context.Context, a Assignment, id string) (realy.Interaction, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.authorize(a.RunID, a.AttemptID, a.LeaseToken); err != nil {
		return realy.Interaction{}, err
	}
	value := s.interactions[id]
	if value == nil || value.RunID != a.RunID {
		return realy.Interaction{}, ErrNotFound
	}
	return *value, nil
}
func (s *MemoryStorage) Interactions(_ context.Context, runID string) ([]realy.Interaction, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.runs[runID] == nil {
		return nil, ErrNotFound
	}
	var values []realy.Interaction
	for _, v := range s.interactions {
		if v.RunID == runID {
			values = append(values, *v)
		}
	}
	sort.Slice(values, func(i, j int) bool { return values[i].CreatedAt.Before(values[j].CreatedAt) })
	return values, nil
}
func (s *MemoryStorage) ResolveInteraction(_ context.Context, id string, response json.RawMessage, tenant, project string) (realy.Interaction, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value := s.interactions[id]
	if value == nil {
		return realy.Interaction{}, ErrNotFound
	}
	run := s.runs[value.RunID]
	if (tenant != "" && run.TenantID != tenant) || (project != "" && run.ProjectID != project) {
		return realy.Interaction{}, ErrNotFound
	}
	if value.State != "pending" {
		return realy.Interaction{}, ErrInvalidTransition
	}
	now := time.Now().UTC()
	value.State = "resolved"
	value.Response = response
	value.ResolvedAt = &now
	s.appendEvent(run, "interaction.resolved", map[string]string{"interaction_id": id})
	return *value, nil
}

func (s *MemoryStorage) authorize(runID, attemptID, lease string) (*realy.Run, error) {
	run := s.runs[runID]
	if run == nil {
		return nil, ErrNotFound
	}
	if run.Attempt.ID != attemptID || lease == "" || run.Attempt.LeaseToken != lease {
		return nil, ErrInvalidLease
	}
	return run, nil
}

func (s *MemoryStorage) releaseNode(nodeID string) {
	if node := s.nodes[nodeID]; node != nil && node.Active > 0 {
		node.Active--
	}
}

func (s *MemoryStorage) requeueExpired(run *realy.Run, now time.Time) {
	if run.Attempt.Status != realy.AttemptLeased || run.Attempt.LeaseExpiresAt == nil || now.Before(*run.Attempt.LeaseExpiresAt) {
		return
	}
	s.releaseNode(run.Attempt.NodeID)
	s.appendEvent(run, "attempt.lease_expired", map[string]string{"node_id": run.Attempt.NodeID})
	run.Attempt.Status, run.Attempt.NodeID, run.Attempt.LeaseToken, run.Attempt.LeaseExpiresAt = realy.AttemptQueued, "", "", nil
}

func (s *MemoryStorage) appendEvent(run *realy.Run, eventType string, value any) {
	data, _ := json.Marshal(value)
	if value == nil {
		data = nil
	}
	s.events[run.ID] = append(s.events[run.ID], realy.Event{ID: newControlPlaneID("event"), RunID: run.ID, AttemptID: run.Attempt.ID, Sequence: len(s.events[run.ID]) + 1, Type: eventType, Data: data, CreatedAt: time.Now().UTC()})
}

func (s *MemoryStorage) cancelRunLocked(run *realy.Run, request CancelRequest, now time.Time) {
	if terminalRun(run.Status) || run.CancelRequestedAt != nil {
		return
	}
	run.CancelRequestedAt = timePointer(now)
	run.CancelReason = request.Reason
	s.appendEvent(run, "run.cancel_requested", request)
	if run.Attempt.Status == realy.AttemptQueued {
		s.finalizeCancellation(run, now)
		return
	}
	run.Status = realy.RunCancelling
}

func (s *MemoryStorage) finalizeCancellation(run *realy.Run, now time.Time) {
	if run.Status == realy.RunCancelled {
		return
	}
	s.releaseNode(run.Attempt.NodeID)
	run.Attempt.Status = realy.AttemptCancelled
	run.Attempt.CompletedAt = timePointer(now)
	run.Status = realy.RunCancelled
	run.CancelledAt = timePointer(now)
	run.CompletedAt = timePointer(now)
	s.appendEvent(run, "attempt.cancelled", map[string]string{"reason": run.CancelReason})
	s.appendEvent(run, "run.cancelled", map[string]string{"reason": run.CancelReason})
}

func terminalRun(status realy.RunStatus) bool {
	return status == realy.RunSucceeded || status == realy.RunFailed || status == realy.RunCancelled
}

func nodeMatches(node Node, request realy.Request) bool {
	foundRuntime := false
	for _, runtime := range node.Runtimes {
		if RuntimeMatches(runtime, request.Runtime) {
			foundRuntime = true
			break
		}
	}
	if !foundRuntime {
		return false
	}
	for key, value := range request.Runtime.Labels {
		if node.Labels[key] != value {
			return false
		}
	}
	for _, grant := range request.Capabilities {
		found := false
		for _, available := range node.Capabilities {
			if available.Name == grant.Name && available.Version == grant.Version {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func newControlPlaneID(prefix string) string {
	var value [12]byte
	if _, err := rand.Read(value[:]); err != nil {
		panic(err)
	}
	return fmt.Sprintf("%s_%s", prefix, hex.EncodeToString(value[:]))
}

func retryBackoff(policy realy.RetryPolicy) time.Duration {
	backoff, _ := time.ParseDuration(policy.Backoff)
	return backoff
}

func timePointer(value time.Time) *time.Time { return &value }
