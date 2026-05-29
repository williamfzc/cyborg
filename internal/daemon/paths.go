// Filesystem paths for managed Cyborg daemon state.
// It centralizes the on-disk layout used by CLI and daemon code.
package daemon

import (
	"fmt"
	"os"
	"path/filepath"
)

func BaseDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	return filepath.Join(home, ".cyborg"), nil
}

func PIDFilePath() (string, error) {
	baseDir, err := BaseDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(baseDir, "daemon.pid"), nil
}

func LogFilePath() (string, error) {
	baseDir, err := BaseDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(baseDir, "daemon.log"), nil
}
