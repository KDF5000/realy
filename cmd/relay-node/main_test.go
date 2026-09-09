package main

import "testing"

func TestProbeRuntimeAdvertisesModels(t *testing.T) {
	runtime := probeRuntime(runtimeConfig{
		Provider: "mock",
		Command:  "/usr/bin/true",
		Model:    "model-default",
		Models:   []string{"model-default", "model-fast"},
	})
	if runtime.DefaultModel != "model-default" {
		t.Fatalf("default model = %q, want model-default", runtime.DefaultModel)
	}
	if len(runtime.Models) != 2 || runtime.Models[1] != "model-fast" {
		t.Fatalf("models = %#v", runtime.Models)
	}
}
