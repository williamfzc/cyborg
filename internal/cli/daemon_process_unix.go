// Unix process controls for the managed Cyborg daemon.
// They keep daemon lifecycle details out of the public CLI flow.
//go:build !windows

package cli

import (
	"os/exec"
	"syscall"
)

func configureManagedDaemonCommand(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func terminateManagedDaemon(pid int) error {
	return syscall.Kill(pid, syscall.SIGTERM)
}
