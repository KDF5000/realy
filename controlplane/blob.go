package controlplane

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync"
)

type MemoryBlobStore struct {
	mu     sync.RWMutex
	values map[string][]byte
}

func NewMemoryBlobStore() *MemoryBlobStore { return &MemoryBlobStore{values: make(map[string][]byte)} }

func (s *MemoryBlobStore) Put(_ context.Context, key string, reader io.Reader) (int64, error) {
	value, err := io.ReadAll(reader)
	if err != nil {
		return 0, err
	}
	s.mu.Lock()
	s.values[key] = value
	s.mu.Unlock()
	return int64(len(value)), nil
}

func (s *MemoryBlobStore) Open(_ context.Context, key string) (io.ReadCloser, error) {
	s.mu.RLock()
	value, ok := s.values[key]
	s.mu.RUnlock()
	if !ok {
		return nil, ErrNotFound
	}
	return io.NopCloser(bytes.NewReader(value)), nil
}

func (s *MemoryBlobStore) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.values[key]; !ok {
		return errors.New("relay blob: not found")
	}
	delete(s.values, key)
	return nil
}
