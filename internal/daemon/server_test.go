// Unit tests for Cyborg daemon behavior.
// They use a fake driver to cover routing without external runtimes.
package daemon

import (
	"context"
	"testing"
	"time"

	"github.com/williamfzc/cyborg/internal/core/action"
	"github.com/williamfzc/cyborg/internal/core/device"
	coredriver "github.com/williamfzc/cyborg/internal/core/driver"
	"github.com/williamfzc/cyborg/internal/store/localstate"
)

// fakeDriver implements coredriver.Driver without external dependencies.
type fakeDriver struct {
	name   string
	engine string
}

func (f *fakeDriver) Summary() coredriver.Summary {
	name := f.name
	if name == "" {
		name = "fake"
	}
	engine := f.engine
	if engine == "" {
		engine = "fake"
	}
	return coredriver.Summary{Name: name, Kind: device.KindBrowser, Engine: engine, Backend: engine}
}

func (f *fakeDriver) Actions() []coredriver.ActionSpec {
	return []coredriver.ActionSpec{
		{Name: "screenshot", Description: "Capture screenshot"},
	}
}

func (f *fakeDriver) Create(_ context.Context, spec coredriver.CreateSpec) (device.Device, error) {
	return device.Device{
		ID:        device.NewID(spec.Kind),
		Kind:      spec.Kind,
		State:     device.StateRunning,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}, nil
}

func (f *fakeDriver) Destroy(_ context.Context, _ device.Device) error { return nil }

func (f *fakeDriver) Act(_ context.Context, _ device.Device, req action.Action) (action.Result, error) {
	return action.Result{OK: true, Result: map[string]any{"action": req.Name, "engine": f.Summary().Engine}}, nil
}

// newTestServer builds a Server backed by a temp-dir store and the fakeDriver.
func newTestServer(t *testing.T) *Server {
	t.Helper()
	dir := t.TempDir()
	store, err := localstate.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	return &Server{
		registry: coredriver.NewRegistry(&fakeDriver{}),
		store:    store,
	}
}

// --- Device resolution tests ---

func TestDevice_NoDevices_EmptyID(t *testing.T) {
	srv := newTestServer(t)
	_, err := srv.Device("")
	if err == nil {
		t.Fatal("expected error when no devices exist")
	}
	if got := err.Error(); got != "no devices available; create one with 'cyborg up'" {
		t.Fatalf("unexpected error message: %s", got)
	}
}

func TestDevice_SingleDevice_EmptyID(t *testing.T) {
	srv := newTestServer(t)

	created, err := srv.Create(context.Background(), device.KindBrowser, nil)
	if err != nil {
		t.Fatal(err)
	}

	resolved, err := srv.Device("")
	if err != nil {
		t.Fatalf("expected auto-resolve but got error: %v", err)
	}
	if resolved.ID != created.ID {
		t.Fatalf("resolved device ID %q != created %q", resolved.ID, created.ID)
	}
}

func TestDevice_MultipleDevices_EmptyID(t *testing.T) {
	srv := newTestServer(t)

	if _, err := srv.Create(context.Background(), device.KindBrowser, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.Create(context.Background(), device.KindBrowser, nil); err != nil {
		t.Fatal(err)
	}

	_, err := srv.Device("")
	if err == nil {
		t.Fatal("expected error when multiple devices exist")
	}
	if got := err.Error(); got != "multiple devices exist; specify --device=<id>" {
		t.Fatalf("unexpected error message: %s", got)
	}
}

func TestDevice_ExplicitID_Found(t *testing.T) {
	srv := newTestServer(t)

	created, err := srv.Create(context.Background(), device.KindBrowser, nil)
	if err != nil {
		t.Fatal(err)
	}

	resolved, err := srv.Device(created.ID)
	if err != nil {
		t.Fatalf("expected found but got error: %v", err)
	}
	if resolved.ID != created.ID {
		t.Fatalf("resolved device ID %q != expected %q", resolved.ID, created.ID)
	}
}

func TestDevice_ExplicitID_NotFound(t *testing.T) {
	srv := newTestServer(t)

	_, err := srv.Device("nonexistent-id")
	if err == nil {
		t.Fatal("expected error for nonexistent device")
	}
	expected := `device "nonexistent-id" not found`
	if got := err.Error(); got != expected {
		t.Fatalf("unexpected error message: %q, want %q", got, expected)
	}
}

// --- Create + ListDevices + Remove lifecycle ---

func TestCreateListRemoveLifecycle(t *testing.T) {
	srv := newTestServer(t)

	// Initially empty.
	devices, err := srv.ListDevices()
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 0 {
		t.Fatalf("expected 0 devices, got %d", len(devices))
	}

	// Create a device.
	created, err := srv.Create(context.Background(), device.KindBrowser, nil)
	if err != nil {
		t.Fatal(err)
	}
	if created.Kind != device.KindBrowser {
		t.Fatalf("expected kind %q, got %q", device.KindBrowser, created.Kind)
	}
	if created.State != device.StateRunning {
		t.Fatalf("expected state %q, got %q", device.StateRunning, created.State)
	}

	// ListDevices should return 1.
	devices, err = srv.ListDevices()
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 1 {
		t.Fatalf("expected 1 device, got %d", len(devices))
	}
	if devices[0].ID != created.ID {
		t.Fatalf("listed device ID %q != created %q", devices[0].ID, created.ID)
	}

	// Remove the device.
	if err := srv.Remove(context.Background(), created.ID); err != nil {
		t.Fatal(err)
	}

	// ListDevices should be empty again.
	devices, err = srv.ListDevices()
	if err != nil {
		t.Fatal(err)
	}
	if len(devices) != 0 {
		t.Fatalf("expected 0 devices after removal, got %d", len(devices))
	}

	// Device resolution should fail.
	_, err = srv.Device("")
	if err == nil {
		t.Fatal("expected error after removing all devices")
	}
}

// --- Full lifecycle with Act ---

func TestCreateActRemoveLifecycle(t *testing.T) {
	srv := newTestServer(t)

	created, err := srv.Create(context.Background(), device.KindBrowser, nil)
	if err != nil {
		t.Fatal(err)
	}

	// Act on the device (auto-resolve via empty DeviceID).
	result, err := srv.Act(context.Background(), action.Action{
		Name:   "screenshot",
		Params: map[string]any{"format": "png"},
	})
	if err != nil {
		t.Fatalf("Act failed: %v", err)
	}
	if !result.OK {
		t.Fatalf("expected OK result, got error: %+v", result.Error)
	}
	payload, ok := result.Result.(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %T", result.Result)
	}
	if payload["action"] != "screenshot" {
		t.Fatalf("expected action=screenshot, got %v", payload["action"])
	}

	// Remove and verify cleanup.
	if err := srv.Remove(context.Background(), created.ID); err != nil {
		t.Fatal(err)
	}
	_, err = srv.Device("")
	if err == nil {
		t.Fatal("expected error after removing device")
	}
}

func TestCreateWithEngineRoutesActionsToThatEngine(t *testing.T) {
	dir := t.TempDir()
	store, err := localstate.New(dir)
	if err != nil {
		t.Fatal(err)
	}
	srv := &Server{
		registry: coredriver.NewRegistry(
			&fakeDriver{name: "default", engine: "default"},
			&fakeDriver{name: "alternate", engine: "alternate"},
		),
		store: store,
	}

	created, err := srv.Create(context.Background(), device.KindBrowser, map[string]any{"engine": "alternate"})
	if err != nil {
		t.Fatal(err)
	}
	if got := created.Metadata["engine"]; got != "alternate" {
		t.Fatalf("expected stored engine alternate, got %v", got)
	}

	result, err := srv.Act(context.Background(), action.Action{DeviceID: created.ID, Name: "screenshot"})
	if err != nil {
		t.Fatalf("Act failed: %v", err)
	}
	payload, ok := result.Result.(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %T", result.Result)
	}
	if payload["engine"] != "alternate" {
		t.Fatalf("expected alternate engine to handle action, got %v", payload["engine"])
	}
}

func TestCreateWithUnknownEngineFails(t *testing.T) {
	srv := newTestServer(t)

	_, err := srv.Create(context.Background(), device.KindBrowser, map[string]any{"engine": "missing"})
	if err == nil {
		t.Fatal("expected unknown engine error")
	}
	if got, want := err.Error(), `no driver registered for kind "browser" with engine "missing"`; got != want {
		t.Fatalf("unexpected error: %q, want %q", got, want)
	}
}
