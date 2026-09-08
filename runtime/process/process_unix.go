//go:build unix

package process

import (
	"os/exec"
	"syscall"
	"time"
)

// Configure makes cancellation terminate the whole subprocess tree. Agent
// runtimes frequently spawn shells and tool processes, so killing only the
// direct child is not sufficient.
func Configure(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error {
		if command.Process == nil {
			return nil
		}
		processGroup := -command.Process.Pid
		if err := syscall.Kill(processGroup, syscall.SIGTERM); err != nil && err != syscall.ESRCH {
			return err
		}
		go func() {
			time.Sleep(3 * time.Second)
			_ = syscall.Kill(processGroup, syscall.SIGKILL)
		}()
		return nil
	}
	command.WaitDelay = 4 * time.Second
}
