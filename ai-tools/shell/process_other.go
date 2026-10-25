//go:build !unix

package shell

import "os/exec"

// Other platforms use exec.CommandContext's direct-process cancellation.
func configureProcess(command *exec.Cmd) {}
