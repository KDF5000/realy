//go:build windows

package process

import "os/exec"

// Configure retains os/exec's default cancellation on Windows. A Job Object
// backed implementation can replace this without changing Runtime adapters.
func Configure(_ *exec.Cmd) {}
