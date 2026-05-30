// End-to-end tests for Cyborg device workflows.
// They exercise agent-facing browser and Android targets through the CLI.
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
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

	// Ensure cleanup happens before the in-process daemon stops.
	defer func() {
		Execute([]string{"rm", dev.ID}, &bytes.Buffer{}, &bytes.Buffer{})
	}()

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

	t.Run("do_open", func(t *testing.T) {
		stdout.Reset()
		stderr.Reset()
		code := Execute([]string{"do", "open", "--url=https://example.com"}, &stdout, &stderr)
		if code != 0 && code != 2 {
			t.Fatalf("open failed (code=%d): stderr=%s stdout=%s", code, stderr.String(), stdout.String())
		}
		assertResultOK(t, stdout.Bytes())
	})

	t.Run("do_eval", func(t *testing.T) {
		time.Sleep(500 * time.Millisecond) // let page load
		stdout.Reset()
		stderr.Reset()
		code := Execute([]string{"do", "eval", "--code=document.title"}, &stdout, &stderr)
		if code != 0 && code != 2 {
			t.Fatalf("eval failed (code=%d): stderr=%s stdout=%s", code, stderr.String(), stdout.String())
		}
		result := assertResultOK(t, stdout.Bytes())
		t.Logf("eval result: %v", result["result"])
	})

	t.Run("do_click_with_target", func(t *testing.T) {
		stdout.Reset()
		stderr.Reset()
		code := Execute([]string{"do", "click", "--device=" + dev.ID, "--target=css:body"}, &stdout, &stderr)
		if code != 0 && code != 2 {
			t.Fatalf("click failed (code=%d): %s", code, stderr.String())
		}
		assertResultOK(t, stdout.Bytes())
	})

	t.Run("help_browser", func(t *testing.T) {
		stdout.Reset()
		stderr.Reset()
		code := Execute([]string{"help", "browser"}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("help browser failed (code=%d): %s", code, stderr.String())
		}
		if !strings.Contains(stdout.String(), "click") {
			t.Fatalf("expected help to list click action, got: %s", stdout.String())
		}
	})
}

// TestE2E_Android runs Android-specific end-to-end tests.
// Requires an adb target or an Android SDK emulator runtime.
func TestE2E_Android(t *testing.T) {
	if os.Getenv("CYBORG_E2E") == "" {
		t.Skip("set CYBORG_E2E=1 to run end-to-end tests")
	}
	if os.Getenv("CYBORG_E2E_ANDROID") == "" {
		t.Skip("set CYBORG_E2E_ANDROID=1 to run android tests")
	}
	if !hasADBTarget() && !hasAndroidEmulatorRuntime() {
		t.Skip("Android E2E requires an adb target or Android SDK emulator runtime")
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

	defer func() {
		Execute([]string{"rm", dev.ID}, &bytes.Buffer{}, &bytes.Buffer{})
	}()

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
		code := Execute([]string{"do", "shell", "--device=" + dev.ID, "--cmd=getprop ro.build.version.sdk"}, &stdout, &stderr)
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
		code := Execute([]string{"do", "tree", "--device=" + dev.ID}, &stdout, &stderr)
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
		code := Execute([]string{"do", "click", "--device=" + dev.ID, "--target=xy:540,1200"}, &stdout, &stderr)
		if code != 0 && code != 2 {
			t.Fatalf("click failed (code=%d): %s", code, stderr.String())
		}
		assertResultOK(t, stdout.Bytes())
	})

	t.Run("click_by_text", func(t *testing.T) {
		// Go home first
		Execute([]string{"do", "press", "--device=" + dev.ID, "--key=home"}, &bytes.Buffer{}, &bytes.Buffer{})
		time.Sleep(1 * time.Second)

		stdout.Reset()
		stderr.Reset()
		code := Execute([]string{"do", "click", "--device=" + dev.ID, "--target=text:Chrome"}, &stdout, &stderr)
		if code == 0 || code == 2 {
			result := assertResultOK(t, stdout.Bytes())
			t.Logf("clicked: %v", result["result"])
		} else {
			t.Logf("text click returned code=%d (may not have the element): %s", code, stdout.String())
		}
	})

	t.Run("swipe", func(t *testing.T) {
		stdout.Reset()
		stderr.Reset()
		code := Execute([]string{"do", "swipe", "--device=" + dev.ID, "--from=540,1500", "--to=540,500"}, &stdout, &stderr)
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

	t.Run("help_android", func(t *testing.T) {
		stdout.Reset()
		stderr.Reset()
		code := Execute([]string{"help", "android"}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("help android failed (code=%d): %s", code, stderr.String())
		}
		if !strings.Contains(stdout.String(), "shell") {
			t.Fatalf("expected help to list shell action, got: %s", stdout.String())
		}
	})
}

// TestE2E_IOS runs iOS Simulator-specific end-to-end tests.
func TestE2E_IOS(t *testing.T) {
	if os.Getenv("CYBORG_E2E") == "" {
		t.Skip("set CYBORG_E2E=1 to run end-to-end tests")
	}
	if os.Getenv("CYBORG_E2E_IOS") == "" {
		t.Skip("set CYBORG_E2E_IOS=1 to run ios simulator tests")
	}
	if _, err := exec.LookPath("xcrun"); err != nil {
		t.Skip("iOS E2E requires xcrun")
	}

	stopDaemon := ensureTestDaemon(t)
	defer stopDaemon()
	cleanupAllDevices(t)

	var stdout, stderr bytes.Buffer
	args := []string{"up", "ios"}
	if udid := os.Getenv("CYBORG_IOS_UDID"); udid != "" {
		args = append(args, "--udid="+udid)
	}
	code := Execute(args, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("up ios failed (code=%d): %s", code, stderr.String())
	}

	var dev device.Device
	if err := json.Unmarshal(stdout.Bytes(), &dev); err != nil {
		t.Fatalf("parse device json: %v\nraw: %s", err, stdout.String())
	}
	t.Logf("created ios device: %s (udid=%v)", dev.ID, dev.Metadata["udid"])

	defer func() {
		Execute([]string{"rm", dev.ID}, &bytes.Buffer{}, &bytes.Buffer{})
	}()

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
		if d.Kind != device.KindIOS {
			t.Fatalf("expected kind ios, got %s", d.Kind)
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

	t.Run("help_ios", func(t *testing.T) {
		stdout.Reset()
		stderr.Reset()
		code := Execute([]string{"help", "ios"}, &stdout, &stderr)
		if code != 0 {
			t.Fatalf("help ios failed (code=%d): %s", code, stderr.String())
		}
		if !strings.Contains(stdout.String(), "launch") {
			t.Fatalf("expected help to list launch action, got: %s", stdout.String())
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

func hasADBTarget() bool {
	adbPath, err := exec.LookPath("adb")
	if err != nil {
		return false
	}
	out, err := exec.Command(adbPath, "devices").Output()
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == "device" {
			return true
		}
	}
	return false
}

func hasAndroidEmulatorRuntime() bool {
	if _, err := exec.LookPath("emulator"); err == nil {
		return true
	}
	candidates := []string{}
	if home := os.Getenv("ANDROID_HOME"); home != "" {
		candidates = append(candidates, home+"/emulator/emulator")
	}
	if home := os.Getenv("ANDROID_SDK_ROOT"); home != "" {
		candidates = append(candidates, home+"/emulator/emulator")
	}
	if home := os.Getenv("HOME"); home != "" {
		candidates = append(candidates, home+"/Library/Android/sdk/emulator/emulator")
	}
	for _, candidate := range candidates {
		if info, err := os.Stat(candidate); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return true
		}
	}
	return false
}
