package realy

import (
	"context"
	"errors"
	"sync"
)

var ErrRunNotFound = errors.New("realy: run not found")

// MemoryStore is a concurrency-safe store useful for embedding, tests, and prototypes.
// Production hosts can implement Store with their own database.
type MemoryStore struct {
	mu        sync.RWMutex
	runs      map[string]Run
	byRequest map[string]string
	events    map[string][]Event
}

func NewMemoryStore() *MemoryStore {
	return &MemoryStore{runs: make(map[string]Run), byRequest: make(map[string]string), events: make(map[string][]Event)}
}

func (s *MemoryStore) Create(_ context.Context, run Run) (Run, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	key := run.TenantID + "\x00" + run.ProjectID + "\x00" + run.IdempotencyKey
	if id, ok := s.byRequest[key]; ok {
		return s.runs[id], false, nil
	}
	s.runs[run.ID] = run
	s.byRequest[key] = run.ID
	return run, true, nil
}

func (s *MemoryStore) Update(_ context.Context, run Run) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.runs[run.ID]; !ok {
		return ErrRunNotFound
	}
	s.runs[run.ID] = run
	return nil
}

func (s *MemoryStore) AppendEvent(_ context.Context, event Event) (Event, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.runs[event.RunID]; !ok {
		return Event{}, ErrRunNotFound
	}
	event.Sequence = len(s.events[event.RunID]) + 1
	s.events[event.RunID] = append(s.events[event.RunID], event)
	return event, nil
}

func (s *MemoryStore) Get(_ context.Context, runID string) (Run, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	run, ok := s.runs[runID]
	if !ok {
		return Run{}, ErrRunNotFound
	}
	return run, nil
}

func (s *MemoryStore) Events(_ context.Context, runID string) ([]Event, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if _, ok := s.runs[runID]; !ok {
		return nil, ErrRunNotFound
	}
	return append([]Event(nil), s.events[runID]...), nil
}
