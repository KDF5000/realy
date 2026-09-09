package controlplane

import (
	"context"

	"github.com/KDF5000/relay"
)

type AccessKind string

const (
	AccessHost AccessKind = "host"
	AccessNode AccessKind = "node"
)

type AccessScope struct {
	Kind                         AccessKind
	Subject, TenantID, ProjectID string
}
type accessKey struct{}

func WithAccess(ctx context.Context, scope AccessScope) context.Context {
	return context.WithValue(ctx, accessKey{}, scope)
}
func AccessFrom(ctx context.Context) (AccessScope, bool) {
	value, ok := ctx.Value(accessKey{}).(AccessScope)
	return value, ok
}
func authorizeRunScope(ctx context.Context, run relay.Run) error {
	scope, ok := AccessFrom(ctx)
	if !ok || scope.Kind == AccessNode {
		return nil
	}
	if scope.TenantID != "" && scope.TenantID != run.TenantID {
		return ErrNotFound
	}
	if scope.ProjectID != "" && scope.ProjectID != run.ProjectID {
		return ErrNotFound
	}
	return nil
}
