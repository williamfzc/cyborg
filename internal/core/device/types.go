// Device model shared across Cyborg packages.
// It defines the execution targets controlled by the daemon.
package device

import (
	"fmt"
	"time"
)

type Kind string

const (
	KindBrowser Kind = "browser"
	KindAndroid Kind = "android"
	KindIOS     Kind = "ios"
	KindVM      Kind = "vm"
)

type State string

const (
	StateCreating State = "creating"
	StateRunning  State = "running"
	StateStopped  State = "stopped"
	StateError    State = "error"
)

type Device struct {
	ID           string         `json:"id"`
	Kind         Kind           `json:"kind"`
	State        State          `json:"state"`
	Capabilities []string       `json:"capabilities"`
	CreatedAt    time.Time      `json:"created_at,omitempty"`
	UpdatedAt    time.Time      `json:"updated_at,omitempty"`
	Metadata     map[string]any `json:"metadata,omitempty"`
}

func NewID(kind Kind) string {
	return fmt.Sprintf("%s-%x", kind, time.Now().UnixNano())
}
