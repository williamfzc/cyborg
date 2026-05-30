// Unit tests for the Android emulator driver.
// They cover local parsing logic without requiring an Android SDK runtime.
package emulator

import "testing"

func TestParseADBDevices(t *testing.T) {
	raw := "List of devices attached\nemulator-5554\tdevice\nemulator-5556\toffline\nabc123\tdevice\n"

	got := parseADBDevices(raw)
	want := []string{"emulator-5554", "abc123"}
	if len(got) != len(want) {
		t.Fatalf("expected %d devices, got %d: %v", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("device %d: expected %q, got %q", i, want[i], got[i])
		}
	}
}

func TestParseAVDList(t *testing.T) {
	raw := "\nPixel_8_API_35\n  Pixel_Tablet_API_35  \n"

	got := parseAVDList(raw)
	want := []string{"Pixel_8_API_35", "Pixel_Tablet_API_35"}
	if len(got) != len(want) {
		t.Fatalf("expected %d avds, got %d: %v", len(want), len(got), got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("avd %d: expected %q, got %q", i, want[i], got[i])
		}
	}
}
