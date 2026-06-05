// In-memory registry for Cyborg device drivers.
// It lets the daemon resolve a backend by device kind.
package driver

import (
	"fmt"
	"sort"

	"github.com/williamfzc/cyborg/internal/core/device"
)

type Registry struct {
	drivers  map[device.Kind]map[string]Driver
	defaults map[device.Kind]string
}

func NewRegistry(drivers ...Driver) *Registry {
	r := &Registry{
		drivers:  make(map[device.Kind]map[string]Driver, len(drivers)),
		defaults: make(map[device.Kind]string, len(drivers)),
	}
	for _, d := range drivers {
		r.Register(d)
	}
	return r
}

func (r *Registry) Register(d Driver) {
	summary := normalizeSummary(d.Summary())
	if r.drivers[summary.Kind] == nil {
		r.drivers[summary.Kind] = map[string]Driver{}
	}
	r.drivers[summary.Kind][summary.Engine] = d
	if r.defaults[summary.Kind] == "" {
		r.defaults[summary.Kind] = summary.Engine
	}
}

func (r *Registry) Get(kind device.Kind) (Driver, error) {
	d, _, err := r.GetEngine(kind, "")
	return d, err
}

func (r *Registry) GetEngine(kind device.Kind, engine string) (Driver, string, error) {
	byEngine, ok := r.drivers[kind]
	if !ok {
		return nil, "", fmt.Errorf("no driver registered for kind %q", kind)
	}
	if engine == "" {
		engine = r.defaults[kind]
	}
	d, ok := byEngine[engine]
	if !ok {
		return nil, "", fmt.Errorf("no driver registered for kind %q with engine %q", kind, engine)
	}
	return d, engine, nil
}

func (r *Registry) Summaries() []Summary {
	var out []Summary
	for _, byEngine := range r.drivers {
		for _, d := range byEngine {
			out = append(out, normalizeSummary(d.Summary()))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind == out[j].Kind {
			if out[i].Engine == out[j].Engine {
				return out[i].Name < out[j].Name
			}
			return out[i].Engine < out[j].Engine
		}
		return out[i].Kind < out[j].Kind
	})
	return out
}

func (r *Registry) Actions(kind device.Kind) ([]ActionSpec, error) {
	return r.ActionsForEngine(kind, "")
}

func (r *Registry) ActionsForEngine(kind device.Kind, engine string) ([]ActionSpec, error) {
	d, _, err := r.GetEngine(kind, engine)
	if err != nil {
		return nil, err
	}
	return d.Actions(), nil
}

func normalizeSummary(summary Summary) Summary {
	if summary.Engine == "" {
		summary.Engine = summary.Backend
	}
	if summary.Engine == "" {
		summary.Engine = summary.Name
	}
	return summary
}
