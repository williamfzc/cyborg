// Local JSON state store for Cyborg.
// It persists devices, sessions, and artifact locations under the user data dir.
package localstate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/williamfzc/cyborg/internal/core/device"
	"github.com/williamfzc/cyborg/internal/core/session"
)

const stateVersion = 1

type State struct {
	Version  int                        `json:"version"`
	Devices  map[string]device.Device   `json:"devices"`
	Sessions map[string]session.Session `json:"sessions"`
}

type Store struct {
	mu           sync.Mutex
	statePath    string
	artifactsDir string
}

func NewDefault() (*Store, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve home dir: %w", err)
	}
	baseDir := filepath.Join(home, ".cyborg")
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		return nil, fmt.Errorf("ensure base dir: %w", err)
	}
	artifactsDir := filepath.Join(baseDir, "artifacts")
	if err := os.MkdirAll(artifactsDir, 0o755); err != nil {
		return nil, fmt.Errorf("ensure artifacts dir: %w", err)
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

func (s *Store) ArtifactsDir() string {
	return s.artifactsDir
}

func (s *Store) Load() (State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.loadUnlocked()
}

func (s *Store) Save(state State) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.saveUnlocked(normalize(state))
}

func (s *Store) Update(fn func(state *State) error) (State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	state, err := s.loadUnlocked()
	if err != nil {
		return State{}, err
	}
	if err := fn(&state); err != nil {
		return State{}, err
	}
	state = normalize(state)
	if err := s.saveUnlocked(state); err != nil {
		return State{}, err
	}
	return state, nil
}

func (s *Store) ListDevices() ([]device.Device, error) {
	state, err := s.Load()
	if err != nil {
		return nil, err
	}
	devices := make([]device.Device, 0, len(state.Devices))
	for _, dev := range state.Devices {
		devices = append(devices, dev)
	}
	sort.Slice(devices, func(i, j int) bool {
		if devices[i].CreatedAt.Equal(devices[j].CreatedAt) {
			return devices[i].ID < devices[j].ID
		}
		return devices[i].CreatedAt.Before(devices[j].CreatedAt)
	})
	return devices, nil
}

func (s *Store) Device(id string) (device.Device, bool, error) {
	state, err := s.Load()
	if err != nil {
		return device.Device{}, false, err
	}
	dev, ok := state.Devices[id]
	return dev, ok, nil
}

func (s *Store) loadUnlocked() (State, error) {
	content, err := os.ReadFile(s.statePath)
	if err != nil {
		if os.IsNotExist(err) {
			state := newState()
			if saveErr := s.saveUnlocked(state); saveErr != nil {
				return State{}, saveErr
			}
			return state, nil
		}
		return State{}, fmt.Errorf("read state file: %w", err)
	}
	state := newState()
	if len(content) == 0 {
		return state, nil
	}
	if err := json.Unmarshal(content, &state); err != nil {
		return State{}, fmt.Errorf("decode state file: %w", err)
	}
	return normalize(state), nil
}

func (s *Store) saveUnlocked(state State) error {
	content, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("encode state file: %w", err)
	}
	tmp := s.statePath + ".tmp"
	if err := os.WriteFile(tmp, content, 0o644); err != nil {
		return fmt.Errorf("write temp state file: %w", err)
	}
	if err := os.Rename(tmp, s.statePath); err != nil {
		return fmt.Errorf("replace state file: %w", err)
	}
	return nil
}

func newState() State {
	return State{
		Version:  stateVersion,
		Devices:  map[string]device.Device{},
		Sessions: map[string]session.Session{},
	}
}

func normalize(state State) State {
	if state.Version == 0 {
		state.Version = stateVersion
	}
	if state.Devices == nil {
		state.Devices = map[string]device.Device{}
	}
	if state.Sessions == nil {
		state.Sessions = map[string]session.Session{}
	}
	for id, dev := range state.Devices {
		if dev.ID == "" {
			dev.ID = id
		}
		if dev.CreatedAt.IsZero() {
			dev.CreatedAt = time.Now()
		}
		if dev.UpdatedAt.IsZero() {
			dev.UpdatedAt = dev.CreatedAt
		}
		state.Devices[id] = dev
	}
	return state
}
