// Core daemon service for Cyborg.
// It owns device state, driver routing, and action dispatch.
package daemon

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/williamfzc/cyborg/internal/buildinfo"
	"github.com/williamfzc/cyborg/internal/core/action"
	"github.com/williamfzc/cyborg/internal/core/device"
	coredriver "github.com/williamfzc/cyborg/internal/core/driver"
	"github.com/williamfzc/cyborg/internal/core/session"
	androiddriver "github.com/williamfzc/cyborg/internal/driver/android/emulator"
	browserdriver "github.com/williamfzc/cyborg/internal/driver/browser/playwright"
	iosdriver "github.com/williamfzc/cyborg/internal/driver/ios/simulator"
	"github.com/williamfzc/cyborg/internal/store/localstate"
)

type Server struct {
	registry *coredriver.Registry
	store    *localstate.Store
}

type Status struct {
	Version  string               `json:"version"`
	Protocol string               `json:"protocol"`
	PID      int                  `json:"pid"`
	Drivers  []coredriver.Summary `json:"drivers"`
	Notes    []string             `json:"notes"`
}

func NewDefaultServer() (*Server, error) {
	store, err := localstate.NewDefault()
	if err != nil {
		return nil, err
	}
	srv := &Server{
		store: store,
		registry: coredriver.NewRegistry(
			browserdriver.New(store),
			androiddriver.New(store),
			iosdriver.New(store),
		),
	}
	if err := srv.reconcileState(); err != nil {
		return nil, err
	}
	return srv, nil
}

func (s *Server) Status() Status {
	return Status{
		Version:  buildinfo.Version,
		Protocol: "localhost-http",
		PID:      os.Getpid(),
		Drivers:  s.registry.Summaries(),
		Notes: []string{
			"daemon starts on demand and exposes a local device control plane over HTTP",
			"browser devices keep live sessions inside the daemon for repeated agent actions",
			"android emulator targets are created or controlled through adb with the same action protocol",
			"ios simulators use simctl for lifecycle actions and WebDriverAgent as the default UI automation bridge",
		},
	}
}

func (s *Server) DriverSummaries() []coredriver.Summary {
	return s.registry.Summaries()
}

func (s *Server) DriverActions(kind device.Kind) ([]coredriver.ActionSpec, error) {
	return s.registry.Actions(kind)
}

func (s *Server) Create(ctx context.Context, kind device.Kind, options map[string]any) (device.Device, error) {
	d, err := s.registry.Get(kind)
	if err != nil {
		return device.Device{}, err
	}
	dev, err := d.Create(ctx, coredriver.CreateSpec{Kind: kind, Options: options})
	if err != nil {
		return device.Device{}, err
	}
	sessionID := fmt.Sprintf("sess-%d", time.Now().UnixNano())
	_, err = s.store.Update(func(state *localstate.State) error {
		state.Devices[dev.ID] = dev
		state.Sessions[sessionID] = session.Session{
			ID:        sessionID,
			DeviceID:  dev.ID,
			CreatedAt: time.Now(),
			Attached:  true,
		}
		return nil
	})
	if err != nil {
		_ = d.Destroy(context.Background(), dev)
		return device.Device{}, err
	}
	return dev, nil
}

func (s *Server) ListDevices() ([]device.Device, error) {
	return s.store.ListDevices()
}

func (s *Server) Device(id string) (device.Device, error) {
	if strings.TrimSpace(id) == "" {
		// Stateless auto-resolve: if exactly one device exists, use it
		devices, err := s.store.ListDevices()
		if err != nil {
			return device.Device{}, err
		}
		switch len(devices) {
		case 0:
			return device.Device{}, fmt.Errorf("no devices available; create one with 'cyborg up'")
		case 1:
			return devices[0], nil
		default:
			return device.Device{}, fmt.Errorf("multiple devices exist; specify --device=<id>")
		}
	}
	dev, ok, err := s.store.Device(id)
	if err != nil {
		return device.Device{}, err
	}
	if !ok {
		return device.Device{}, fmt.Errorf("device %q not found", id)
	}
	return dev, nil
}

func (s *Server) Remove(ctx context.Context, id string) error {
	dev, err := s.Device(id)
	if err != nil {
		return err
	}
	d, err := s.registry.Get(dev.Kind)
	if err != nil {
		return err
	}
	if err := d.Destroy(ctx, dev); err != nil {
		return err
	}
	_, err = s.store.Update(func(state *localstate.State) error {
		delete(state.Devices, dev.ID)
		for sid, sess := range state.Sessions {
			if sess.DeviceID == dev.ID {
				delete(state.Sessions, sid)
			}
		}
		return nil
	})
	return err
}

func (s *Server) Act(ctx context.Context, req action.Action) (action.Result, error) {
	dev, err := s.Device(req.DeviceID)
	if err != nil {
		return action.Result{OK: false, Error: &action.Error{Code: "DEVICE_NOT_FOUND", Message: err.Error()}}, nil
	}
	if req.DeviceID == "" {
		req.DeviceID = dev.ID
	}
	d, err := s.registry.Get(dev.Kind)
	if err != nil {
		return action.Result{}, err
	}
	result, err := d.Act(ctx, dev, req)
	if err != nil {
		return action.Result{}, err
	}
	dev.UpdatedAt = time.Now()
	if !result.OK && result.Error != nil && result.Error.Code == "DEVICE_UNAVAILABLE" {
		dev.State = device.StateStopped
	}
	if _, updateErr := s.store.Update(func(state *localstate.State) error {
		if _, ok := state.Devices[dev.ID]; ok {
			state.Devices[dev.ID] = dev
		}
		return nil
	}); updateErr != nil {
		return action.Result{}, updateErr
	}
	return result, nil
}

func (s *Server) reconcileState() error {
	_, err := s.store.Update(func(state *localstate.State) error {
		for id, dev := range state.Devices {
			if dev.Kind == device.KindBrowser && dev.State == device.StateRunning {
				dev.State = device.StateStopped
				dev.UpdatedAt = time.Now()
				if dev.Metadata == nil {
					dev.Metadata = map[string]any{}
				}
				dev.Metadata["recovered"] = false
				state.Devices[id] = dev
			}
		}
		return nil
	})
	return err
}
