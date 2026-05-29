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

type Driver interface {
	Summary() Summary
	Create(ctx context.Context, spec CreateSpec) (device.Device, error)
	Destroy(ctx context.Context, dev device.Device) error
	Act(ctx context.Context, dev device.Device, action action.Action) (action.Result, error)
}
