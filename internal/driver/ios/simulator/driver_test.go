// Unit tests for the iOS Simulator driver.
// They cover driver metadata and local parsing logic without requiring Xcode.
package simulator

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/williamfzc/cyborg/internal/core/action"
	"github.com/williamfzc/cyborg/internal/core/device"
)

func TestSummaryDescribesIOSDriver(t *testing.T) {
	d := New(nil)
	summary := d.Summary()

	if summary.Kind != device.KindIOS {
		t.Fatalf("expected kind %q, got %q", device.KindIOS, summary.Kind)
	}
	if summary.Backend == "" {
		t.Fatal("expected backend to be described")
	}
	if len(summary.Capabilities) == 0 {
		t.Fatal("expected capabilities to be listed")
	}
}

func TestNormalizeWDAURLDefaultsToLocalAgent(t *testing.T) {
	got, err := normalizeWDAURL("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != defaultWDAURL {
		t.Fatalf("expected default WDA URL %q, got %q", defaultWDAURL, got)
	}
	got, err = normalizeWDAURL("http://localhost:8100/")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "http://localhost:8100" {
		t.Fatalf("expected trailing slash to be trimmed, got %q", got)
	}
}

func TestNormalizeWDAURLRejectsNonLoopbackHosts(t *testing.T) {
	if _, err := normalizeWDAURL("http://example.com:8100"); err == nil {
		t.Fatal("expected non-loopback WDA URL to be rejected")
	}
}

func TestIOSCapabilitiesIncludeDefaultUICapabilities(t *testing.T) {
	capabilities := iosCapabilities()
	for _, want := range []string{"screenshot", "tree", "click"} {
		if !contains(capabilities, want) {
			t.Fatalf("expected capability %q in %v", want, capabilities)
		}
	}
}

func TestProbeWDA(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/status" {
			t.Fatalf("expected /status probe, got %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"value":{"ready":true}}`))
	}))
	defer server.Close()

	ok, status := probeWDA(context.Background(), server.Client(), server.URL)
	if !ok || status != "reachable" {
		t.Fatalf("expected reachable WDA, got ok=%v status=%q", ok, status)
	}

	client := &http.Client{Timeout: 10 * time.Millisecond}
	ok, status = probeWDA(context.Background(), client, "http://127.0.0.1:1")
	if ok || status == "" {
		t.Fatalf("expected unavailable WDA with status reason, got ok=%v status=%q", ok, status)
	}
}

func TestLocatorPayload(t *testing.T) {
	tests := []struct {
		name      string
		target    action.Target
		wantUsing string
		wantValue string
		wantErr   bool
	}{
		{
			name:      "text predicate",
			target:    action.Target{Strategy: action.StrategyText, Value: "Login"},
			wantUsing: "predicate string",
			wantValue: "label == 'Login' OR name == 'Login' OR value == 'Login'",
		},
		{
			name:      "accessibility id",
			target:    action.Target{Strategy: action.StrategyAcc, Value: "Submit"},
			wantUsing: "accessibility id",
			wantValue: "Submit",
		},
		{
			name:    "unsupported css",
			target:  action.Target{Strategy: action.StrategyCSS, Value: "#submit"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotUsing, gotValue, err := locatorPayload(tt.target)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if gotUsing != tt.wantUsing || gotValue != tt.wantValue {
				t.Fatalf("got (%q, %q), want (%q, %q)", gotUsing, gotValue, tt.wantUsing, tt.wantValue)
			}
		})
	}
}

func TestParsePoint(t *testing.T) {
	x, y, err := parsePoint("120,300")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if x != 120 || y != 300 {
		t.Fatalf("got (%d, %d), want (120, 300)", x, y)
	}

	if _, _, err := parsePoint("bad"); err == nil {
		t.Fatal("expected error for invalid point")
	}
}

func TestParseAvailableSimulators(t *testing.T) {
	raw := `{
		"devices": {
			"com.apple.CoreSimulator.SimRuntime.iOS-26-4": [
				{"udid": "A", "name": "iPhone 17 Pro", "state": "Shutdown", "isAvailable": true},
				{"udid": "B", "name": "iPad", "state": "Shutdown", "isAvailable": false},
				{"udid": "C", "name": "iPhone SE", "state": "Shutdown", "isAvailable": true}
			]
		}
	}`

	got, err := parseAvailableSimulators(raw, "iPhone SE")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].UDID != "C" {
		t.Fatalf("expected selected simulator C, got: %+v", got)
	}
}

func contains(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
