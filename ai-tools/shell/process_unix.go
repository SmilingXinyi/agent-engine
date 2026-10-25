//go:build unix

package shell

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// Cancel the command's process group so ordinary child processes do not survive
// a timeout while retaining inherited stdout/stderr pipes.
func configureProcess(command *exec.Cmd) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error {
		err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			return os.ErrProcessDone
		}
		return err
	}
}
