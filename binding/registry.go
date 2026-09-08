package binding

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	"github.com/KDF5000/realy"
)

var ErrBindingNotFound = errors.New("realy: capability binding not found")

type Descriptor struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Kind    string `json:"kind"`
}

type entry struct {
	descriptor Descriptor
	provider   realy.CapabilityProvider
}

// Registry routes capability calls to user-provided CLI, HTTP, RPC, or in-process bindings.
type Registry struct {
	mu      sync.RWMutex
	entries map[string]entry
}

func NewRegistry() *Registry { return &Registry{entries: make(map[string]entry)} }

func (r *Registry) Register(descriptor Descriptor, provider realy.CapabilityProvider) error {
	if descriptor.Name == "" || descriptor.Version == "" || descriptor.Kind == "" || provider == nil {
		return errors.New("realy: binding name, version, kind, and provider are required")
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries[key(descriptor.Name, descriptor.Version)] = entry{descriptor: descriptor, provider: provider}
	return nil
}

func (r *Registry) Invoke(ctx context.Context, request realy.CapabilityRequest) (json.RawMessage, error) {
	r.mu.RLock()
	registered, ok := r.entries[key(request.Name, request.Version)]
	r.mu.RUnlock()
	if !ok {
		return nil, ErrBindingNotFound
	}
	return registered.provider.Invoke(ctx, request)
}

func (r *Registry) Inventory() []Descriptor {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]Descriptor, 0, len(r.entries))
	for _, registered := range r.entries {
		result = append(result, registered.descriptor)
	}
	return result
}

func key(name, version string) string { return name + "@" + version }
