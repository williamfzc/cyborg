// Managed daemon process helpers shared by the CLI.
// Platform-specific files provide the process control primitives.
package cli

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/williamfzc/cyborg/internal/daemon"
)

func startManagedDaemon(logFile *os.File) error {
	cmd := exec.Command(os.Args[0], "daemon", "serve")
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	configureManagedDaemonCommand(cmd)
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() {
		_ = cmd.Wait()
		_ = logFile.Close()
	}()
	return nil
}

func stopManagedDaemon(statusPID int) error {
	pid := statusPID
	if pid == 0 {
		var err error
		pid, err = managedDaemonPIDFromFile()
		if err != nil {
			return nil
		}
	}
	if err := terminateManagedDaemon(pid); err != nil {
		return err
	}
	time.Sleep(300 * time.Millisecond)
	if pidPath, err := daemon.PIDFilePath(); err == nil {
		_ = os.Remove(pidPath)
	}
	return nil
}

func managedDaemonPIDFromFile() (int, error) {
	pidPath, err := daemon.PIDFilePath()
	if err != nil {
		return 0, err
	}
	content, err := os.ReadFile(pidPath)
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(string(content)))
}
