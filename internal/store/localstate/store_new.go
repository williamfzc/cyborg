// Configurable local state store constructor for Cyborg.
// It lets tests isolate state in temporary directories.
package localstate

import (
	"os"
	"path/filepath"
)

// New creates a Store rooted at baseDir. Useful for testing with isolated directories.
func New(baseDir string) (*Store, error) {
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		return nil, err
	}
	artifactsDir := filepath.Join(baseDir, "artifacts")
	if err := os.MkdirAll(artifactsDir, 0o755); err != nil {
		return nil, err
	}
	store := &Store{
		statePath:    filepath.Join(baseDir, "state.json"),
		artifactsDir: artifactsDir,
	}
	if _, err := os.Stat(store.statePath); os.IsNotExist(err) {
		if err := store.Save(newState()); err != nil {
			return nil, err
		}
	}
	return store, nil
}
