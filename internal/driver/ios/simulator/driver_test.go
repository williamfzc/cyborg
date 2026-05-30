// Unit tests for the iOS Simulator driver.
// They cover driver metadata and local parsing logic without requiring Xcode.
package simulator

import (
	"testing"

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
