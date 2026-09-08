package controlplane_test

import (
	"testing"

	"github.com/KDF5000/realy"
	"github.com/KDF5000/realy/controlplane"
)

func TestRuntimeMatchesAdvertisedModel(t *testing.T) {
	runtime := controlplane.Runtime{ID: "node-a/codex", Provider: "codex", State: "healthy", DefaultModel: "model-default", Models: []string{"model-default", "model-fast"}}
	if !controlplane.RuntimeMatches(runtime, realy.RuntimeRequirement{Provider: "codex", Model: "model-fast"}) {
		t.Fatal("advertised model should match")
	}
	if controlplane.RuntimeMatches(runtime, realy.RuntimeRequirement{Provider: "codex", Model: "model-large"}) {
		t.Fatal("unadvertised model should not match a runtime with a model catalog")
	}
}

func TestRuntimeMatchesExactInstance(t *testing.T) {
	runtime := controlplane.Runtime{ID: "node-a/codex", Provider: "codex", State: "healthy"}
	if !controlplane.RuntimeMatches(runtime, realy.RuntimeRequirement{ID: "node-a/codex", Provider: "codex"}) {
		t.Fatal("requested runtime instance should match")
	}
	if controlplane.RuntimeMatches(runtime, realy.RuntimeRequirement{ID: "node-b/codex", Provider: "codex"}) {
		t.Fatal("a different runtime instance must not match")
	}
	if !controlplane.RuntimeMatches(runtime, realy.RuntimeRequirement{Provider: "codex"}) {
		t.Fatal("an unbound requirement should match any compatible instance")
	}
}

func TestRuntimeWithoutCatalogAcceptsModelOverride(t *testing.T) {
	runtime := controlplane.Runtime{Provider: "trae", State: "healthy"}
	if !controlplane.RuntimeMatches(runtime, realy.RuntimeRequirement{Provider: "trae", Model: "custom-model"}) {
		t.Fatal("runtime without a model catalog should defer validation to its provider")
	}
}
