// Android adb driver for Cyborg.
// It maps generic actions onto real devices or emulators attached through adb.
package emulator

import (
	"context"
	"encoding/xml"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/williamfzc/cyborg/internal/core/action"
	"github.com/williamfzc/cyborg/internal/core/device"
	coredriver "github.com/williamfzc/cyborg/internal/core/driver"
	"github.com/williamfzc/cyborg/internal/store/localstate"
)

type liveSession struct {
	serial      string
	artifactDir string
}

type Driver struct {
	store    *localstate.Store
	mu       sync.Mutex
	sessions map[string]*liveSession
}

func New(store *localstate.Store) *Driver {
	return &Driver{
		store:    store,
		sessions: map[string]*liveSession{},
	}
}

func (d *Driver) Summary() coredriver.Summary {
	return coredriver.Summary{
		Name:    "android-emulator",
		Kind:    device.KindAndroid,
		Backend: "android/adb",
		Capabilities: []string{
			"click",
			"type",
			"press",
			"screenshot",
			"tree",
			"shell",
			"install",
			"swipe",
		},
		Notes: []string{
			"Connects to adb-attached devices (real or emulator)",
			"Use --serial to specify a device, or auto-detects if only one is connected",
		},
	}
}

func (d *Driver) Create(ctx context.Context, spec coredriver.CreateSpec) (device.Device, error) {
	adbPath, err := findADB()
	if err != nil {
		return device.Device{}, err
	}

	serial := stringOption(spec.Options, "serial")
	if serial == "" {
		serial, err = autoDetectDevice(ctx, adbPath)
		if err != nil {
			return device.Device{}, err
		}
	}

	// Verify device is online
	out, err := adbCmd(ctx, adbPath, serial, "get-state")
	if err != nil {
		return device.Device{}, fmt.Errorf("device %q not reachable: %w", serial, err)
	}
	state := strings.TrimSpace(out)
	if state != "device" {
		return device.Device{}, fmt.Errorf("device %q is in state %q (expected 'device')", serial, state)
	}

	// Gather device info
	model, _ := adbShell(ctx, adbPath, serial, "getprop", "ro.product.model")
	sdk, _ := adbShell(ctx, adbPath, serial, "getprop", "ro.build.version.sdk")
	android, _ := adbShell(ctx, adbPath, serial, "getprop", "ro.build.version.release")

	now := time.Now()
	artifactDir := filepath.Join(d.store.ArtifactsDir(), fmt.Sprintf("%s-%d", spec.Kind, now.UnixNano()))
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		return device.Device{}, fmt.Errorf("create artifact dir: %w", err)
	}

	dev := device.Device{
		ID:    device.NewID(device.KindAndroid),
		Kind:  device.KindAndroid,
		State: device.StateRunning,
		Capabilities: []string{
			"click", "type", "press", "screenshot",
			"tree", "shell", "install", "swipe",
		},
		CreatedAt: now,
		UpdatedAt: now,
		Metadata: map[string]any{
			"driver":          "android-adb",
			"backend":         "android/adb",
			"serial":          serial,
			"model":           strings.TrimSpace(model),
			"sdk_version":     strings.TrimSpace(sdk),
			"android_version": strings.TrimSpace(android),
			"artifact_dir":    artifactDir,
			"adb_path":        adbPath,
		},
	}

	d.mu.Lock()
	d.sessions[dev.ID] = &liveSession{
		serial:      serial,
		artifactDir: artifactDir,
	}
	d.mu.Unlock()

	return dev, nil
}

func (d *Driver) Destroy(_ context.Context, dev device.Device) error {
	d.mu.Lock()
	session, ok := d.sessions[dev.ID]
	if ok {
		delete(d.sessions, dev.ID)
	}
	d.mu.Unlock()

	artifactDir := ""
	if session != nil {
		artifactDir = session.artifactDir
	} else if dir, ok := dev.Metadata["artifact_dir"].(string); ok {
		artifactDir = dir
	}
	if artifactDir != "" {
		_ = os.RemoveAll(artifactDir)
	}
	return nil
}

func (d *Driver) Act(ctx context.Context, dev device.Device, req action.Action) (action.Result, error) {
	session, err := d.session(dev.ID)
	if err != nil {
		return action.Result{OK: false, Error: &action.Error{Code: "DEVICE_UNAVAILABLE", Message: err.Error()}}, nil
	}

	adbPath, _ := dev.Metadata["adb_path"].(string)
	if adbPath == "" {
		adbPath, _ = findADB()
	}

	if req.TimeoutMs > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(req.TimeoutMs)*time.Millisecond)
		defer cancel()
	}

	switch normalizeActionName(req.Name) {
	case "click":
		return d.actClick(ctx, adbPath, session, req)
	case "type":
		return d.actType(ctx, adbPath, session, req)
	case "press":
		return d.actPress(ctx, adbPath, session, req)
	case "screenshot":
		return d.actScreenshot(ctx, adbPath, session, req)
	case "tree":
		return d.actTree(ctx, adbPath, session)
	case "shell":
		return d.actShell(ctx, adbPath, session, req)
	case "install":
		return d.actInstall(ctx, adbPath, session, req)
	case "swipe":
		return d.actSwipe(ctx, adbPath, session, req)
	default:
		return action.Result{OK: false, Error: &action.Error{
			Code:    "ACTION_UNSUPPORTED",
			Message: fmt.Sprintf("unsupported action %q for android device", req.Name),
		}}, nil
	}
}

// --- action implementations ---

func (d *Driver) actClick(ctx context.Context, adbPath string, session *liveSession, req action.Action) (action.Result, error) {
	// Support both coordinate-based and selector-based click
	selector := stringParam(req.Params, "selector")
	if selector != "" {
		return d.clickBySelector(ctx, adbPath, session, selector)
	}
	xStr := stringParam(req.Params, "x")
	yStr := stringParam(req.Params, "y")
	if xStr != "" && yStr != "" {
		_, err := adbShell(ctx, adbPath, session.serial, "input", "tap", xStr, yStr)
		if err != nil {
			return runtimeError("CLICK_FAILED", err), nil
		}
		return action.Result{OK: true, Result: map[string]any{"x": xStr, "y": yStr}}, nil
	}
	return invalidParam("missing --selector or --x/--y"), nil
}

func (d *Driver) clickBySelector(ctx context.Context, adbPath string, session *liveSession, selector string) (action.Result, error) {
	node, err := d.findNode(ctx, adbPath, session, selector)
	if err != nil {
		return runtimeError("ELEMENT_NOT_FOUND", err), nil
	}
	x, y := node.center()
	_, err = adbShell(ctx, adbPath, session.serial, "input", "tap", strconv.Itoa(x), strconv.Itoa(y))
	if err != nil {
		return runtimeError("CLICK_FAILED", err), nil
	}
	return action.Result{OK: true, Result: map[string]any{
		"selector": selector,
		"x":        x,
		"y":        y,
	}}, nil
}

func (d *Driver) actType(ctx context.Context, adbPath string, session *liveSession, req action.Action) (action.Result, error) {
	text := stringParam(req.Params, "text")
	if text == "" {
		return invalidParam("missing --text"), nil
	}
	// Escape special characters for adb input
	escaped := strings.ReplaceAll(text, " ", "%s")
	escaped = strings.ReplaceAll(escaped, "&", "\\&")
	escaped = strings.ReplaceAll(escaped, "<", "\\<")
	escaped = strings.ReplaceAll(escaped, ">", "\\>")
	escaped = strings.ReplaceAll(escaped, "'", "\\'")
	escaped = strings.ReplaceAll(escaped, "\"", "\\\"")

	_, err := adbShell(ctx, adbPath, session.serial, "input", "text", escaped)
	if err != nil {
		return runtimeError("TYPE_FAILED", err), nil
	}
	return action.Result{OK: true, Result: map[string]any{"text": text}}, nil
}

func (d *Driver) actPress(ctx context.Context, adbPath string, session *liveSession, req action.Action) (action.Result, error) {
	key := stringParam(req.Params, "key")
	if key == "" {
		return invalidParam("missing --key"), nil
	}
	keycode := resolveKeycode(key)
	_, err := adbShell(ctx, adbPath, session.serial, "input", "keyevent", keycode)
	if err != nil {
		return runtimeError("PRESS_FAILED", err), nil
	}
	return action.Result{OK: true, Result: map[string]any{"key": key, "keycode": keycode}}, nil
}

func (d *Driver) actScreenshot(ctx context.Context, adbPath string, session *liveSession, req action.Action) (action.Result, error) {
	path := stringParam(req.Params, "path")
	if path == "" {
		path = filepath.Join(session.artifactDir, fmt.Sprintf("screenshot-%d.png", time.Now().UnixNano()))
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return runtimeError("ARTIFACT_WRITE_FAILED", err), nil
	}

	// Use exec-out for binary-safe output
	cmd := exec.CommandContext(ctx, adbPath, "-s", session.serial, "exec-out", "screencap", "-p")
	data, err := cmd.Output()
	if err != nil {
		return runtimeError("SCREENSHOT_FAILED", err), nil
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return runtimeError("ARTIFACT_WRITE_FAILED", err), nil
	}
	artifact := action.Artifact{Kind: "screenshot", Path: path, Name: filepath.Base(path)}
	return action.Result{OK: true, Artifacts: []action.Artifact{artifact}, Result: map[string]any{"path": path}}, nil
}

func (d *Driver) actTree(ctx context.Context, adbPath string, session *liveSession) (action.Result, error) {
	xmlContent, err := dumpUIHierarchy(ctx, adbPath, session.serial)
	if err != nil {
		return runtimeError("TREE_FAILED", err), nil
	}
	return action.Result{OK: true, Result: map[string]any{"xml": xmlContent}}, nil
}

func (d *Driver) actShell(ctx context.Context, adbPath string, session *liveSession, req action.Action) (action.Result, error) {
	cmdStr := stringParam(req.Params, "cmd")
	if cmdStr == "" {
		return invalidParam("missing --cmd"), nil
	}
	// Pass command as single arg; adb shell interprets it on device
	cmd := exec.CommandContext(ctx, adbPath, "-s", session.serial, "shell", cmdStr)
	outBytes, err := cmd.CombinedOutput()
	if err != nil {
		return runtimeError("SHELL_FAILED", fmt.Errorf("%s: %s", err, strings.TrimSpace(string(outBytes)))), nil
	}
	return action.Result{OK: true, Result: map[string]any{"output": strings.TrimSpace(string(outBytes))}}, nil
}

func (d *Driver) actInstall(ctx context.Context, adbPath string, session *liveSession, req action.Action) (action.Result, error) {
	apk := stringParam(req.Params, "apk")
	if apk == "" {
		return invalidParam("missing --apk"), nil
	}
	cmd := exec.CommandContext(ctx, adbPath, "-s", session.serial, "install", "-r", apk)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return runtimeError("INSTALL_FAILED", fmt.Errorf("%s: %w", strings.TrimSpace(string(out)), err)), nil
	}
	return action.Result{OK: true, Result: map[string]any{"apk": apk, "output": strings.TrimSpace(string(out))}}, nil
}

func (d *Driver) actSwipe(ctx context.Context, adbPath string, session *liveSession, req action.Action) (action.Result, error) {
	from := stringParam(req.Params, "from")
	to := stringParam(req.Params, "to")
	if from == "" || to == "" {
		return invalidParam("missing --from or --to (format: x,y)"), nil
	}
	fromParts := strings.SplitN(from, ",", 2)
	toParts := strings.SplitN(to, ",", 2)
	if len(fromParts) != 2 || len(toParts) != 2 {
		return invalidParam("--from and --to must be in format x,y"), nil
	}
	duration := stringParam(req.Params, "duration")
	if duration == "" {
		duration = "300"
	}
	args := []string{"input", "swipe", fromParts[0], fromParts[1], toParts[0], toParts[1], duration}
	_, err := adbShell(ctx, adbPath, session.serial, args...)
	if err != nil {
		return runtimeError("SWIPE_FAILED", err), nil
	}
	return action.Result{OK: true, Result: map[string]any{"from": from, "to": to, "duration_ms": duration}}, nil
}

// --- UI element finding via uiautomator ---

type uiNode struct {
	Text        string   `xml:"text,attr"`
	ResourceID  string   `xml:"resource-id,attr"`
	ContentDesc string   `xml:"content-desc,attr"`
	ClassName   string   `xml:"class,attr"`
	Bounds      string   `xml:"bounds,attr"`
	Children    []uiNode `xml:"node"`
}

func (n *uiNode) center() (int, int) {
	// bounds format: [x1,y1][x2,y2]
	re := regexp.MustCompile(`\[(\d+),(\d+)\]\[(\d+),(\d+)\]`)
	m := re.FindStringSubmatch(n.Bounds)
	if len(m) != 5 {
		return 0, 0
	}
	x1, _ := strconv.Atoi(m[1])
	y1, _ := strconv.Atoi(m[2])
	x2, _ := strconv.Atoi(m[3])
	y2, _ := strconv.Atoi(m[4])
	return (x1 + x2) / 2, (y1 + y2) / 2
}

type uiHierarchy struct {
	XMLName xml.Name `xml:"hierarchy"`
	Nodes   []uiNode `xml:"node"`
}

func (d *Driver) findNode(ctx context.Context, adbPath string, session *liveSession, selector string) (*uiNode, error) {
	xmlContent, err := dumpUIHierarchy(ctx, adbPath, session.serial)
	if err != nil {
		return nil, err
	}

	var hierarchy uiHierarchy
	if err := xml.Unmarshal([]byte(xmlContent), &hierarchy); err != nil {
		return nil, fmt.Errorf("parse UI hierarchy: %w", err)
	}

	key, value := parseSelector(selector)
	var found *uiNode
	walkNodes(hierarchy.Nodes, func(n *uiNode) bool {
		if matchNode(n, key, value) {
			found = n
			return false
		}
		return true
	})
	if found == nil {
		return nil, fmt.Errorf("no element matching selector %q", selector)
	}
	return found, nil
}

func parseSelector(selector string) (string, string) {
	// Supported formats:
	//   text=Sign In
	//   resource-id=com.example:id/btn
	//   content-desc=Submit
	//   class=android.widget.Button
	// Default: match by text
	for _, prefix := range []string{"text=", "resource-id=", "content-desc=", "class="} {
		if strings.HasPrefix(selector, prefix) {
			return strings.TrimSuffix(prefix, "="), selector[len(prefix):]
		}
	}
	return "text", selector
}

func matchNode(n *uiNode, key, value string) bool {
	switch key {
	case "text":
		return n.Text == value
	case "resource-id":
		return n.ResourceID == value
	case "content-desc":
		return n.ContentDesc == value
	case "class":
		return n.ClassName == value
	}
	return false
}

func walkNodes(nodes []uiNode, fn func(*uiNode) bool) bool {
	for i := range nodes {
		if !fn(&nodes[i]) {
			return false
		}
		if !walkNodes(nodes[i].Children, fn) {
			return false
		}
	}
	return true
}

// --- helpers ---

func (d *Driver) session(deviceID string) (*liveSession, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	session, ok := d.sessions[deviceID]
	if !ok {
		return nil, fmt.Errorf("device session not live; recreate with 'cyborg up android'")
	}
	return session, nil
}

func dumpUIHierarchy(ctx context.Context, adbPath, serial string) (string, error) {
	// Dump to device temp file then pull content
	remotePath := "/sdcard/window_dump.xml"
	_, err := adbShell(ctx, adbPath, serial, "uiautomator", "dump", remotePath)
	if err != nil {
		return "", fmt.Errorf("uiautomator dump: %w", err)
	}
	out, err := adbShell(ctx, adbPath, serial, "cat", remotePath)
	if err != nil {
		return "", fmt.Errorf("read dump: %w", err)
	}
	return out, nil
}

func findADB() (string, error) {
	// Try PATH first
	if p, err := exec.LookPath("adb"); err == nil {
		return p, nil
	}
	// Common locations
	candidates := []string{
		"/opt/homebrew/bin/adb",
		"/usr/local/bin/adb",
	}
	if home := os.Getenv("ANDROID_HOME"); home != "" {
		candidates = append([]string{filepath.Join(home, "platform-tools", "adb")}, candidates...)
	}
	if home := os.Getenv("HOME"); home != "" {
		candidates = append(candidates, filepath.Join(home, "Library/Android/sdk/platform-tools/adb"))
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c, nil
		}
	}
	return "", fmt.Errorf("adb not found; install Android SDK platform-tools or set ANDROID_HOME")
}

func autoDetectDevice(ctx context.Context, adbPath string) (string, error) {
	cmd := exec.CommandContext(ctx, adbPath, "devices")
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("adb devices: %w", err)
	}
	var serials []string
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "List") || strings.HasPrefix(line, "*") {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) >= 2 && parts[1] == "device" {
			serials = append(serials, parts[0])
		}
	}
	switch len(serials) {
	case 0:
		return "", fmt.Errorf("no adb devices connected; connect a device or start an emulator, then retry")
	case 1:
		return serials[0], nil
	default:
		return "", fmt.Errorf("multiple adb devices connected (%s); specify --serial=<serial>", strings.Join(serials, ", "))
	}
}

func adbCmd(ctx context.Context, adbPath, serial string, args ...string) (string, error) {
	fullArgs := append([]string{"-s", serial}, args...)
	cmd := exec.CommandContext(ctx, adbPath, fullArgs...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s: %s", err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func adbShell(ctx context.Context, adbPath, serial string, args ...string) (string, error) {
	fullArgs := append([]string{"-s", serial, "shell"}, args...)
	cmd := exec.CommandContext(ctx, adbPath, fullArgs...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s: %s", err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func normalizeActionName(name string) string {
	return strings.ReplaceAll(strings.ReplaceAll(name, "-", "_"), " ", "_")
}

func resolveKeycode(key string) string {
	keycodes := map[string]string{
		"enter":       "KEYCODE_ENTER",
		"back":        "KEYCODE_BACK",
		"home":        "KEYCODE_HOME",
		"menu":        "KEYCODE_MENU",
		"tab":         "KEYCODE_TAB",
		"delete":      "KEYCODE_DEL",
		"backspace":   "KEYCODE_DEL",
		"escape":      "KEYCODE_ESCAPE",
		"up":          "KEYCODE_DPAD_UP",
		"down":        "KEYCODE_DPAD_DOWN",
		"left":        "KEYCODE_DPAD_LEFT",
		"right":       "KEYCODE_DPAD_RIGHT",
		"space":       "KEYCODE_SPACE",
		"power":       "KEYCODE_POWER",
		"volume_up":   "KEYCODE_VOLUME_UP",
		"volume_down": "KEYCODE_VOLUME_DOWN",
	}
	lower := strings.ToLower(key)
	if code, ok := keycodes[lower]; ok {
		return code
	}
	// If already a KEYCODE_*, pass through
	if strings.HasPrefix(strings.ToUpper(key), "KEYCODE_") {
		return strings.ToUpper(key)
	}
	// Numeric keycode
	if _, err := strconv.Atoi(key); err == nil {
		return key
	}
	return "KEYCODE_" + strings.ToUpper(key)
}

func stringOption(options map[string]any, key string) string {
	if options == nil {
		return ""
	}
	if v, ok := options[key]; ok {
		s, _ := v.(string)
		return s
	}
	return ""
}

func stringParam(params map[string]any, key string) string {
	if params == nil {
		return ""
	}
	if v, ok := params[key]; ok {
		switch val := v.(type) {
		case string:
			return val
		case int:
			return strconv.Itoa(val)
		case float64:
			return strconv.FormatFloat(val, 'f', -1, 64)
		default:
			return fmt.Sprintf("%v", v)
		}
	}
	return ""
}

func invalidParam(message string) action.Result {
	return action.Result{OK: false, Error: &action.Error{Code: "INVALID_ARGUMENT", Message: message}}
}

func runtimeError(code string, err error) action.Result {
	return action.Result{OK: false, Error: &action.Error{Code: code, Message: err.Error()}}
}
