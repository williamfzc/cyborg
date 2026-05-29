// Driver contract for Cyborg device backends.
// It keeps daemon orchestration separate from backend implementation.
package driver

import (
	"context"

	"github.com/williamfzc/cyborg/internal/core/action"
	"github.com/williamfzc/cyborg/internal/core/device"
)

type Summary struct {
	Name         string      `json:"name"`
	Kind         device.Kind `json:"kind"`
	Backend      string      `json:"backend"`
	Capabilities []string    `json:"capabilities"`
	Notes        []string    `json:"notes,omitempty"`
}

type CreateSpec struct {
	Kind    device.Kind    `json:"kind"`
	Options map[string]any `json:"options,omitempty"`
}

// ParamSpec describes a single parameter for an action.
type ParamSpec struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Required    bool   `json:"required"`
}

// ActionSpec describes an action a driver supports.
type ActionSpec struct {
	Name        string      `json:"name"`
	Description string      `json:"description"`
	Params      []ParamSpec `json:"params"`
}

type Driver interface {
	Summary() Summary
	Actions() []ActionSpec
	Create(ctx context.Context, spec CreateSpec) (device.Device, error)
	Destroy(ctx context.Context, dev device.Device) error
	Act(ctx context.Context, dev device.Device, action action.Action) (action.Result, error)
}
