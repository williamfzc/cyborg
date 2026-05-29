// End-to-end tests for Cyborg device workflows.
// They exercise real browser and Android devices through the CLI.
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/williamfzc/cyborg/internal/core/device"
	"github.com/williamfzc/cyborg/internal/daemon"
	daemonclient "github.com/williamfzc/cyborg/internal/daemon/client"
)

// TestE2E runs all end-to-end tests sequentially with a single browser instance.
// Only one browser is ever created to avoid sandbox process accumulation.
func TestE2E(t *testing.T) {
	if os.Getenv("CYBORG_E2E") == "" {
		t.Skip("set CYBORG_E2E=1 to run end-to-end tests (requires Chrome/Chromium)")
	}

	stopDaemon := ensureTestDaemon(t)
	defer stopDaemon()
	cleanupAllDevices(t)

	// --- Create ONE browser device ---
	var stdout, stderr bytes.Buffer
	code := Execute([]string{"up", "browser", "--headless=true"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("up browser failed (code=%d): %s", code, stderr.String())
	}

	var dev device.Device
	if err := json.Unmarshal(stdout.Bytes(), &dev); err != nil {
		t.Fatalf("parse device json: %v\nraw: %s", err, stdout.String())
	}
	t.Logf("created device: %s (kind=%s)", dev.ID, dev.Kind)

	// Ensure cleanup happens no matter what
	t.Cleanup(func() {
		Execute([]string{"rm", dev.ID}, &bytes.Buffer{}, &bytes.Buffer{})
	})

	// --- Subtests all share the single browser ---
	t.Run("ls", func(t *testing.T) {
		stdout.Reset()
		stderr.Reset()
		code := Execute([]string{"ls"}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("ls failed: %s", stderr.String())
		}
		var devices []device.Device
		if err := json.Unmarshal(stdout.Bytes(), &devices); err != nil {
			t.Fatalf("parse: %v", err)
		}
		if len(devices) != 1 {
			t.Fatalf("expected 1 device, got %d", len(devices))
		}
	})

	t.Run("show", func(t *testing.T) {
		stdout.Reset()
		stderr.Reset()
		code := Execute([]string{"show", dev.ID}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("show failed: %s", stderr.String())
		}
	})

	t.Run("do_screenshot", func(t *testing.T) {
		stdout.Reset()
		stderr.Reset()
		code := Execute([]string{"do", "screenshot"}, &stdout, &stderr)
		if code != 0 && code != 2 {
			t.Fatalf("screenshot failed (code=%d): %s", code, stderr.String())
		}
		assertResultOK(t, stdout.Bytes())
	})

	t.Run("browser_open", func(t *testing.T) {
		stdout.Reset()
		stderr.Reset()
		code := Execute([]string{"browser", "open", "--url=https://example.com"}, &stdout, &stderr)
		if code != 0 && code != 2 {
			t.Fatalf("browser open failed (code=%d): stderr=%s stdout=%s", code, stderr.String(), stdout.String())
		}
		assertResultOK(t, stdout.Bytes())
	})

	t.Run("browser_eval", func(t *testing.T) {
		time.Sleep(500 * time.Millisecond) // let page load
		stdout.Reset()
		stderr.Reset()
		code := Execute([]string{"browser", "eval", "--code=document.title"}, &stdout, &stderr)
		if code != 0 && code != 2 {
			t.Fatalf("eval failed (code=%d): stderr=%s stdout=%s", code, stderr.String(), stdout.String())
		}
		result := assertResultOK(t, stdout.Bytes())
		t.Logf("eval result: %v", result["result"])
	})

	t.Run("do_click_explicit_device", func(t *testing.T) {
		stdout.Reset()
		stderr.Reset()
		code := Execute([]string{"do", "click", "--device=" + dev.ID, "--selector=body"}, &stdout, &stderr)
		if code != 0 && code != 2 {
			t.Fatalf("click failed (code=%d): %s", code, stderr.String())
		}
		assertResultOK(t, stdout.Bytes())
	})

	t.Run("kind_mismatch_rejected", func(t *testing.T) {
		stdout.Reset()
		stderr.Reset()
		code := Execute([]string{"android", "tree", "--device=" + dev.ID}, &stdout, &stderr)
		if code == 0 {
			t.Fatal("expected android command on browser device to fail")
		}
		if !strings.Contains(stderr.String(), "android") {
			t.Logf("unexpected error: %s", stderr.String())
		}
	})
}

// TestE2E_Android runs Android-specific end-to-end tests.
// Requires: adb device connected (real or emulator).
func TestE2E_Android(t *testing.T) {
	if os.Getenv("CYBORG_E2E") == "" {
		t.Skip("set CYBORG_E2E=1 to run end-to-end tests")
	}
	if os.Getenv("CYBORG_E2E_ANDROID") == "" {
		t.Skip("set CYBORG_E2E_ANDROID=1 to run android tests (requires adb device)")
	}

	stopDaemon := ensureTestDaemon(t)
	defer stopDaemon()
	cleanupAllDevices(t)

	// --- Create Android device (auto-detect or specific serial) ---
	var stdout, stderr bytes.Buffer
	args := []string{"up", "android"}
	if serial := os.Getenv("CYBORG_ANDROID_SERIAL"); serial != "" {
		args = append(args, "--serial="+serial)
	}
	code := Execute(args, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("up android failed (code=%d): %s", code, stderr.String())
	}

	var dev device.Device
	if err := json.Unmarshal(stdout.Bytes(), &dev); err != nil {
		t.Fatalf("parse device json: %v\nraw: %s", err, stdout.String())
	}
	t.Logf("created android device: %s (serial=%v)", dev.ID, dev.Metadata["serial"])

	t.Cleanup(func() {
		Execute([]string{"rm", dev.ID}, &bytes.Buffer{}, &bytes.Buffer{})
	})

	t.Run("show", func(t *testing.T) {
		stdout.Reset()
		stderr.Reset()
		code := Execute([]string{"show", dev.ID}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("show failed: %s", stderr.String())
		}
		var d device.Device
		if err := json.Unmarshal(stdout.Bytes(), &d); err != nil {
			t.Fatalf("parse: %v", err)
		}
		if d.Kind != device.KindAndroid {
			t.Fatalf("expected kind android, got %s", d.Kind)
		}
	})

	t.Run("screenshot", func(t *testing.T) {
		stdout.Reset()
		stderr.Reset()
		code := Execute([]string{"do", "screenshot", "--device=" + dev.ID}, &stdout, &stderr)
		if code != 0 && code != 2 {
			t.Fatalf("screenshot failed (code=%d): %s", code, stderr.String())
		}
		result := assertResultOK(t, stdout.Bytes())
		t.Logf("screenshot: %v", result["artifacts"])
	})

	t.Run("shell", func(t *testing.T) {
		stdout.Reset()
		stderr.Reset()
		code := Execute([]string{"android", "shell", "--device=" + dev.ID, "--cmd=getprop ro.build.version.sdk"}, &stdout, &stderr)
		if code != 0 && code != 2 {
			t.Fatalf("shell failed (code=%d): %s", code, stderr.String())
		}
		result := assertResultOK(t, stdout.Bytes())
		resultMap, _ := result["result"].(map[string]any)
		output, _ := resultMap["output"].(string)
		t.Logf("sdk version: %s", output)
		if output == "" {
			t.Fatal("expected non-empty sdk version")
		}
	})

	t.Run("tree", func(t *testing.T) {
		stdout.Reset()
		stderr.Reset()
		code := Execute([]string{"android", "tree", "--device=" + dev.ID}, &stdout, &stderr)
		if code != 0 && code != 2 {
			t.Fatalf("tree failed (code=%d): %s", code, stderr.String())
		}
		result := assertResultOK(t, stdout.Bytes())
		resultMap, _ := result["result"].(map[string]any)
		xmlStr, _ := resultMap["xml"].(string)
		if !strings.Contains(xmlStr, "hierarchy") {
			t.Fatal("expected XML hierarchy in tree output")
		}
		t.Logf("tree XML length: %d bytes", len(xmlStr))
	})

	t.Run("press_home", func(t *testing.T) {
		stdout.Reset()
		stderr.Reset()
		code := Execute([]string{"do", "press", "--device=" + dev.ID, "--key=home"}, &stdout, &stderr)
		if code != 0 && code != 2 {
			t.Fatalf("press failed (code=%d): %s", code, stderr.String())
		}
		assertResultOK(t, stdout.Bytes())
	})

	t.Run("click_by_coordinate", func(t *testing.T) {
		stdout.Reset()
		stderr.Reset()
		code := Execute([]string{"do", "click", "--device=" + dev.ID, "--x=540", "--y=1200"}, &stdout, &stderr)
		if code != 0 && code != 2 {
			t.Fatalf("click failed (code=%d): %s", code, stderr.String())
		}
		assertResultOK(t, stdout.Bytes())
	})

	t.Run("click_by_selector", func(t *testing.T) {
		// Go home first
		Execute([]string{"do", "press", "--device=" + dev.ID, "--key=home"}, &bytes.Buffer{}, &bytes.Buffer{})
		time.Sleep(1 * time.Second)

		stdout.Reset()
		stderr.Reset()
		// Try clicking something visible on home screen
		code := Execute([]string{"do", "click", "--device=" + dev.ID, "--selector=Chrome"}, &stdout, &stderr)
		if code == 0 || code == 2 {
			// Selector found and clicked
			result := assertResultOK(t, stdout.Bytes())
			t.Logf("clicked: %v", result["result"])
		} else {
			// If Chrome isn't on home screen, that's fine
			t.Logf("selector click returned code=%d (may not have the element): %s", code, stdout.String())
		}
	})

	t.Run("swipe", func(t *testing.T) {
		stdout.Reset()
		stderr.Reset()
		code := Execute([]string{"android", "swipe", "--device=" + dev.ID, "--from=540,1500", "--to=540,500"}, &stdout, &stderr)
		if code != 0 && code != 2 {
			t.Fatalf("swipe failed (code=%d): %s", code, stderr.String())
		}
		assertResultOK(t, stdout.Bytes())
	})

	t.Run("type_text", func(t *testing.T) {
		stdout.Reset()
		stderr.Reset()
		code := Execute([]string{"do", "type", "--device=" + dev.ID, "--text=hello"}, &stdout, &stderr)
		if code != 0 && code != 2 {
			t.Fatalf("type failed (code=%d): %s", code, stderr.String())
		}
		assertResultOK(t, stdout.Bytes())
	})

	t.Run("browser_kind_mismatch", func(t *testing.T) {
		stdout.Reset()
		stderr.Reset()
		code := Execute([]string{"browser", "open", "--device=" + dev.ID, "--url=https://example.com"}, &stdout, &stderr)
		if code == 0 {
			t.Fatal("expected browser command on android device to fail")
		}
		if !strings.Contains(stderr.String(), "browser") {
			t.Logf("error: %s", stderr.String())
		}
	})
}

// --- helpers ---

func ensureTestDaemon(t *testing.T) func() {
	t.Helper()
	client := daemonclient.NewDefault()

	// Check if daemon is already running
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	_, err := client.Status(ctx)
	cancel()
	if err == nil {
		return func() {} // already running, don't stop it
	}

	srv, err := daemon.NewDefaultServer()
	if err != nil {
		t.Fatalf("init daemon: %v", err)
	}

	srvCtx, srvCancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = srv.ListenAndServe(srvCtx, daemon.DefaultAddress)
		close(done)
	}()

	// Wait for ready
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		if _, err := client.Status(ctx); err == nil {
			cancel()
			return func() {
				srvCancel()
				<-done
			}
		}
		cancel()
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("daemon did not start in time")
	return nil
}

func cleanupAllDevices(t *testing.T) {
	t.Helper()
	var stdout bytes.Buffer
	code := Execute([]string{"ls"}, &stdout, &bytes.Buffer{})
	if code != 0 {
		return
	}
	var devices []device.Device
	if err := json.Unmarshal(stdout.Bytes(), &devices); err != nil {
		return
	}
	for _, dev := range devices {
		Execute([]string{"rm", dev.ID}, &bytes.Buffer{}, &bytes.Buffer{})
	}
}

func assertResultOK(t *testing.T, data []byte) map[string]any {
	t.Helper()
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("parse result json: %v\nraw: %s", err, string(data))
	}
	if ok, _ := result["ok"].(bool); !ok {
		t.Fatalf("action returned ok=false: %s", string(data))
	}
	return result
}
