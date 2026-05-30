// Windows process controls for the managed Cyborg daemon.
// They preserve the same on-demand daemon model without installing a service.
//go:build windows

package cli

import (
	"os/exec"
	"strconv"
)

func configureManagedDaemonCommand(_ *exec.Cmd) {
}

func terminateManagedDaemon(pid int) error {
	return exec.Command("taskkill", "/PID", strconv.Itoa(pid), "/T", "/F").Run()
}
