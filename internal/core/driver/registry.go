// In-memory registry for Cyborg device drivers.
// It lets the daemon resolve a backend by device kind.
package driver

import (
	"fmt"
	"sort"

	"github.com/williamfzc/cyborg/internal/core/device"
)

type Registry struct {
	drivers map[device.Kind]Driver
}

func NewRegistry(drivers ...Driver) *Registry {
	r := &Registry{drivers: make(map[device.Kind]Driver, len(drivers))}
	for _, d := range drivers {
		r.Register(d)
	}
	return r
}

func (r *Registry) Register(d Driver) {
	r.drivers[d.Summary().Kind] = d
}

func (r *Registry) Get(kind device.Kind) (Driver, error) {
	d, ok := r.drivers[kind]
	if !ok {
		return nil, fmt.Errorf("no driver registered for kind %q", kind)
	}
	return d, nil
}

func (r *Registry) Summaries() []Summary {
	out := make([]Summary, 0, len(r.drivers))
	for _, d := range r.drivers {
		out = append(out, d.Summary())
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind == out[j].Kind {
			return out[i].Name < out[j].Name
		}
		return out[i].Kind < out[j].Kind
	})
	return out
}
