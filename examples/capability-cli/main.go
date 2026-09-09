// Command relay-example-capability demonstrates the protocol implemented by a
// user-owned CLI capability binding.
package main

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/KDF5000/relay"
)

func main() {
	var request relay.CapabilityRequest
	if err := json.NewDecoder(os.Stdin).Decode(&request); err != nil {
		fail("decode request: %v", err)
	}
	if request.Name != "issue.read" || request.Version != "1" {
		fail("unsupported capability %s@%s", request.Name, request.Version)
	}
	if request.Resource == "" {
		fail("resource is required")
	}
	response := map[string]any{
		"output": map[string]any{
			"id":     request.Resource,
			"title":  "Fix flaky scheduler",
			"source": "user-cli",
		},
	}
	if err := json.NewEncoder(os.Stdout).Encode(response); err != nil {
		fail("encode response: %v", err)
	}
}

func fail(format string, values ...any) {
	_ = json.NewEncoder(os.Stdout).Encode(map[string]string{"error": fmt.Sprintf(format, values...)})
	os.Exit(1)
}
