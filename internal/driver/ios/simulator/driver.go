// iOS Simulator driver for Cyborg.
// It uses simctl for simulator lifecycle actions and optional WebDriverAgent for UI automation.
package simulator

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/williamfzc/cyborg/internal/core/action"
	"github.com/williamfzc/cyborg/internal/core/device"
	coredriver "github.com/williamfzc/cyborg/internal/core/driver"
	"github.com/williamfzc/cyborg/internal/store/localstate"
)

const defaultWDAURL = "http://127.0.0.1:8100"

var (
	baseCapabilities = []string{"screenshot", "install", "launch", "terminate"}
	uiCapabilities   = []string{"click", "type", "press", "swipe", "tree"}
)

type liveSession struct {
	udid        string
	artifactDir string
	wdaURL      string
	owned       bool
	httpClient  *http.Client
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
		Name:    "ios-simulator",
		Kind:    device.KindIOS,
		Engine:  "wda",
		Backend: "ios/simctl+wda",
		Capabilities: []string{
			"screenshot", "install", "launch", "terminate",
			"click", "type", "press", "swipe", "tree",
		},
		Notes: []string{
			"Creates or connects to an iOS Simulator via xcrun simctl",
			"Use --udid to specify a simulator, or auto-selects an available simulator",
			"UI actions use WebDriverAgent at http://127.0.0.1:8100 by default; pass --wda-url to override",
			"target strategies: text (default), id, acc, xy",
		},
	}
}

func (d *Driver) Actions() []coredriver.ActionSpec {
	return []coredriver.ActionSpec{
		{
			Name:        "screenshot",
			Description: "Capture simulator screen",
			Params:      []coredriver.ParamSpec{{Name: "path", Description: "Output file path (auto-generated if omitted)"}},
		},
		{
			Name:        "install",
			Description: "Install an iOS app bundle",
			Params:      []coredriver.ParamSpec{{Name: "app", Description: "Path to .app bundle", Required: true}},
		},
		{
			Name:        "launch",
			Description: "Launch an installed app",
			Params:      []coredriver.ParamSpec{{Name: "bundle_id", Description: "App bundle identifier", Required: true}},
		},
		{
			Name:        "terminate",
			Description: "Terminate a running app",
			Params:      []coredriver.ParamSpec{{Name: "bundle_id", Description: "App bundle identifier", Required: true}},
		},
		{
			Name:        "click",
			Description: "Tap an element or coordinate",
			Params:      []coredriver.ParamSpec{{Name: "target", Description: "Element locator (text:label | id:name | acc:label | xy:x,y)", Required: true}},
		},
		{
			Name:        "type",
			Description: "Type text into the focused field",
			Params:      []coredriver.ParamSpec{{Name: "text", Description: "Text to type", Required: true}},
		},
		{
			Name:        "press",
			Description: "Press an iOS control key",
			Params:      []coredriver.ParamSpec{{Name: "key", Description: "Key name (home)", Required: true}},
		},
		{
			Name:        "swipe",
			Description: "Swipe gesture between two points",
			Params: []coredriver.ParamSpec{
				{Name: "from", Description: "Start coordinates (x,y)", Required: true},
				{Name: "to", Description: "End coordinates (x,y)", Required: true},
				{Name: "duration", Description: "Duration in seconds (default 0.3)"},
			},
		},
		{
			Name:        "tree",
			Description: "Dump UI hierarchy XML",
			Params:      nil,
		},
	}
}

func (d *Driver) Create(ctx context.Context, spec coredriver.CreateSpec) (device.Device, error) {
	xcrunPath, err := findXcrun()
	if err != nil {
		return device.Device{}, err
	}

	udid := stringOption(spec.Options, "udid")
	owned := false
	if udid == "" {
		udid, owned, err = resolveSimulator(ctx, xcrunPath, stringOption(spec.Options, "name"))
		if err != nil {
			return device.Device{}, err
		}
	} else if err := bootSimulator(ctx, xcrunPath, udid); err == nil {
		owned = true
	}
	if err := verifyBootedSimulator(ctx, xcrunPath, udid); err != nil {
		return device.Device{}, err
	}

	now := time.Now()
	artifactDir := filepath.Join(d.store.ArtifactsDir(), fmt.Sprintf("%s-%d", spec.Kind, now.UnixNano()))
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		return device.Device{}, fmt.Errorf("create artifact dir: %w", err)
	}

	wdaURL, err := normalizeWDAURL(stringOption(spec.Options, "wda_url"))
	if err != nil {
		return device.Device{}, err
	}
	httpClient := &http.Client{Timeout: 30 * time.Second}
	wdaAvailable, wdaStatus := probeWDA(ctx, httpClient, wdaURL)
	dev := device.Device{
		ID:           device.NewID(device.KindIOS),
		Kind:         device.KindIOS,
		State:        device.StateRunning,
		Capabilities: iosCapabilities(),
		CreatedAt:    now,
		UpdatedAt:    now,
		Metadata: map[string]any{
			"driver":        "ios-simulator",
			"backend":       "ios/simctl+wda",
			"udid":          udid,
			"xcrun_path":    xcrunPath,
			"artifact_dir":  artifactDir,
			"wda_url":       wdaURL,
			"wda_available": wdaAvailable,
			"wda_status":    wdaStatus,
			"owned":         owned,
		},
	}

	d.mu.Lock()
	d.sessions[dev.ID] = &liveSession{
		udid:        udid,
		artifactDir: artifactDir,
		wdaURL:      wdaURL,
		owned:       owned,
		httpClient:  httpClient,
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
		d.removeArtifactDir(artifactDir)
	}
	owned := false
	if session != nil {
		owned = session.owned
	} else if value, ok := dev.Metadata["owned"].(bool); ok {
		owned = value
	}
	udid := ""
	if session != nil {
		udid = session.udid
	} else if value, ok := dev.Metadata["udid"].(string); ok {
		udid = value
	}
	xcrunPath, _ := dev.Metadata["xcrun_path"].(string)
	if owned && udid != "" {
		if xcrunPath == "" {
			xcrunPath, _ = findXcrun()
		}
		if xcrunPath != "" {
			_, _ = simctl(context.Background(), xcrunPath, "shutdown", udid)
		}
	}
	return nil
}

func (d *Driver) Act(ctx context.Context, dev device.Device, req action.Action) (action.Result, error) {
	session, err := d.session(dev.ID)
	if err != nil {
		return action.Result{OK: false, Error: &action.Error{Code: "DEVICE_UNAVAILABLE", Message: err.Error()}}, nil
	}

	xcrunPath, _ := dev.Metadata["xcrun_path"].(string)
	if xcrunPath == "" {
		xcrunPath, _ = findXcrun()
	}
	if req.TimeoutMs > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(req.TimeoutMs)*time.Millisecond)
		defer cancel()
	}

	switch normalizeActionName(req.Name) {
	case "screenshot":
		return d.actScreenshot(ctx, xcrunPath, session, req)
	case "install":
		return d.actInstall(ctx, xcrunPath, session, req)
	case "launch":
		return d.actLaunch(ctx, xcrunPath, session, req)
	case "terminate":
		return d.actTerminate(ctx, xcrunPath, session, req)
	case "click":
		return d.actClick(ctx, session, req)
	case "type":
		return d.actType(ctx, session, req)
	case "press":
		return d.actPress(ctx, session, req)
	case "swipe":
		return d.actSwipe(ctx, session, req)
	case "tree":
		return d.actTree(ctx, session)
	default:
		return action.Result{OK: false, Error: &action.Error{
			Code:    "ACTION_UNSUPPORTED",
			Message: fmt.Sprintf("unsupported action %q for ios device", req.Name),
		}}, nil
	}
}

func (d *Driver) actScreenshot(ctx context.Context, xcrunPath string, session *liveSession, req action.Action) (action.Result, error) {
	path := stringParam(req.Params, "path")
	if path == "" {
		path = filepath.Join(session.artifactDir, fmt.Sprintf("screenshot-%d.png", time.Now().UnixNano()))
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return runtimeError("ARTIFACT_WRITE_FAILED", err), nil
	}
	if _, err := simctl(ctx, xcrunPath, "io", session.udid, "screenshot", path); err != nil {
		return runtimeError("SCREENSHOT_FAILED", err), nil
	}
	artifact := action.Artifact{Kind: "screenshot", Path: path, Name: filepath.Base(path)}
	return action.Result{OK: true, Artifacts: []action.Artifact{artifact}, Result: map[string]any{"path": path}}, nil
}

func (d *Driver) actInstall(ctx context.Context, xcrunPath string, session *liveSession, req action.Action) (action.Result, error) {
	app := stringParam(req.Params, "app")
	if app == "" {
		app = stringParam(req.Params, "ipa")
	}
	if app == "" {
		return invalidParam("missing --app"), nil
	}
	out, err := simctl(ctx, xcrunPath, "install", session.udid, app)
	if err != nil {
		return runtimeError("INSTALL_FAILED", err), nil
	}
	return action.Result{OK: true, Result: map[string]any{"app": app, "output": strings.TrimSpace(out)}}, nil
}

func (d *Driver) actLaunch(ctx context.Context, xcrunPath string, session *liveSession, req action.Action) (action.Result, error) {
	bundleID := stringParam(req.Params, "bundle_id")
	if bundleID == "" {
		bundleID = stringParam(req.Params, "bundle")
	}
	if bundleID == "" {
		return invalidParam("missing --bundle-id"), nil
	}
	out, err := simctl(ctx, xcrunPath, "launch", session.udid, bundleID)
	if err != nil {
		return runtimeError("LAUNCH_FAILED", err), nil
	}
	return action.Result{OK: true, Result: map[string]any{"bundle_id": bundleID, "output": strings.TrimSpace(out)}}, nil
}

func (d *Driver) actTerminate(ctx context.Context, xcrunPath string, session *liveSession, req action.Action) (action.Result, error) {
	bundleID := stringParam(req.Params, "bundle_id")
	if bundleID == "" {
		bundleID = stringParam(req.Params, "bundle")
	}
	if bundleID == "" {
		return invalidParam("missing --bundle-id"), nil
	}
	out, err := simctl(ctx, xcrunPath, "terminate", session.udid, bundleID)
	if err != nil {
		return runtimeError("TERMINATE_FAILED", err), nil
	}
	return action.Result{OK: true, Result: map[string]any{"bundle_id": bundleID, "output": strings.TrimSpace(out)}}, nil
}

func (d *Driver) actClick(ctx context.Context, session *liveSession, req action.Action) (action.Result, error) {
	if err := requireWDA(session); err != nil {
		return runtimeError("WDA_UNAVAILABLE", err), nil
	}
	target := stringParam(req.Params, "target")
	if target == "" {
		target = stringParam(req.Params, "selector")
	}
	if target == "" {
		return invalidParam("missing --target (e.g. text:Login, acc:Submit, id:button, xy:120,300)"), nil
	}

	wdaSessionID, err := ensureWDASession(ctx, session)
	if err != nil {
		return runtimeError("WDA_SESSION_FAILED", err), nil
	}
	t := action.ParseTarget(target, action.StrategyText)
	if t.Strategy == action.StrategyXY {
		x, y, err := parsePoint(t.Value)
		if err != nil {
			return invalidParam(err.Error()), nil
		}
		if _, err := wdaRequest(ctx, session, http.MethodPost, "/session/"+wdaSessionID+"/wda/tap/0", map[string]any{"x": x, "y": y}); err != nil {
			return runtimeError("CLICK_FAILED", err), nil
		}
		return action.Result{OK: true, Result: map[string]any{"x": x, "y": y}}, nil
	}

	elementID, err := findElement(ctx, session, wdaSessionID, t)
	if err != nil {
		return runtimeError("ELEMENT_NOT_FOUND", err), nil
	}
	if _, err := wdaRequest(ctx, session, http.MethodPost, "/session/"+wdaSessionID+"/element/"+elementID+"/click", map[string]any{}); err != nil {
		return runtimeError("CLICK_FAILED", err), nil
	}
	return action.Result{OK: true, Result: map[string]any{"target": target, "element_id": elementID}}, nil
}

func (d *Driver) actType(ctx context.Context, session *liveSession, req action.Action) (action.Result, error) {
	if err := requireWDA(session); err != nil {
		return runtimeError("WDA_UNAVAILABLE", err), nil
	}
	text := stringParam(req.Params, "text")
	if text == "" {
		return invalidParam("missing --text"), nil
	}
	wdaSessionID, err := ensureWDASession(ctx, session)
	if err != nil {
		return runtimeError("WDA_SESSION_FAILED", err), nil
	}
	if _, err := wdaRequest(ctx, session, http.MethodPost, "/session/"+wdaSessionID+"/keys", map[string]any{"value": []string{text}}); err != nil {
		return runtimeError("TYPE_FAILED", err), nil
	}
	return action.Result{OK: true, Result: map[string]any{"text": text}}, nil
}

func (d *Driver) actPress(ctx context.Context, session *liveSession, req action.Action) (action.Result, error) {
	if err := requireWDA(session); err != nil {
		return runtimeError("WDA_UNAVAILABLE", err), nil
	}
	key := strings.ToLower(stringParam(req.Params, "key"))
	if key == "" {
		return invalidParam("missing --key"), nil
	}
	wdaSessionID, err := ensureWDASession(ctx, session)
	if err != nil {
		return runtimeError("WDA_SESSION_FAILED", err), nil
	}
	switch key {
	case "home":
		if _, err := wdaRequest(ctx, session, http.MethodPost, "/session/"+wdaSessionID+"/wda/homescreen", map[string]any{}); err != nil {
			return runtimeError("PRESS_FAILED", err), nil
		}
	default:
		return invalidParam("unsupported iOS key; use home"), nil
	}
	return action.Result{OK: true, Result: map[string]any{"key": key}}, nil
}

func (d *Driver) actSwipe(ctx context.Context, session *liveSession, req action.Action) (action.Result, error) {
	if err := requireWDA(session); err != nil {
		return runtimeError("WDA_UNAVAILABLE", err), nil
	}
	from := stringParam(req.Params, "from")
	to := stringParam(req.Params, "to")
	if from == "" || to == "" {
		return invalidParam("missing --from or --to (format: x,y)"), nil
	}
	fromX, fromY, err := parsePoint(from)
	if err != nil {
		return invalidParam("--from " + err.Error()), nil
	}
	toX, toY, err := parsePoint(to)
	if err != nil {
		return invalidParam("--to " + err.Error()), nil
	}
	duration := floatOption(req.Params, "duration", 0.3)
	wdaSessionID, err := ensureWDASession(ctx, session)
	if err != nil {
		return runtimeError("WDA_SESSION_FAILED", err), nil
	}
	payload := map[string]any{
		"fromX": fromX, "fromY": fromY,
		"toX": toX, "toY": toY,
		"duration": duration,
	}
	if _, err := wdaRequest(ctx, session, http.MethodPost, "/session/"+wdaSessionID+"/wda/dragfromtoforduration", payload); err != nil {
		return runtimeError("SWIPE_FAILED", err), nil
	}
	return action.Result{OK: true, Result: map[string]any{"from": from, "to": to, "duration": duration}}, nil
}

func (d *Driver) actTree(ctx context.Context, session *liveSession) (action.Result, error) {
	if err := requireWDA(session); err != nil {
		return runtimeError("WDA_UNAVAILABLE", err), nil
	}
	wdaSessionID, err := ensureWDASession(ctx, session)
	if err != nil {
		return runtimeError("WDA_SESSION_FAILED", err), nil
	}
	resp, err := wdaRequest(ctx, session, http.MethodGet, "/session/"+wdaSessionID+"/source", nil)
	if err != nil {
		return runtimeError("TREE_FAILED", err), nil
	}
	return action.Result{OK: true, Result: map[string]any{"xml": stringValue(resp["value"])}}, nil
}

func (d *Driver) session(deviceID string) (*liveSession, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	session, ok := d.sessions[deviceID]
	if !ok {
		return nil, fmt.Errorf("device session not live; recreate with 'cyborg up ios'")
	}
	return session, nil
}

func findXcrun() (string, error) {
	if p, err := exec.LookPath("xcrun"); err == nil {
		return p, nil
	}
	return "", fmt.Errorf("xcrun not found; install Xcode command line tools")
}

type simctlDeviceList struct {
	Devices map[string][]simctlDevice `json:"devices"`
}

type simctlDevice struct {
	UDID        string `json:"udid"`
	Name        string `json:"name"`
	State       string `json:"state"`
	IsAvailable bool   `json:"isAvailable"`
}

func autoDetectBootedSimulator(ctx context.Context, xcrunPath string) (string, error) {
	out, err := simctl(ctx, xcrunPath, "list", "devices", "booted", "-j")
	if err != nil {
		return "", err
	}
	var list simctlDeviceList
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		return "", fmt.Errorf("parse simctl device list: %w", err)
	}
	var booted []simctlDevice
	for _, devices := range list.Devices {
		for _, dev := range devices {
			if dev.State == "Booted" && dev.IsAvailable {
				booted = append(booted, dev)
			}
		}
	}
	switch len(booted) {
	case 0:
		return "", fmt.Errorf("no booted iOS Simulator found")
	case 1:
		return booted[0].UDID, nil
	default:
		names := make([]string, 0, len(booted))
		for _, dev := range booted {
			names = append(names, fmt.Sprintf("%s(%s)", dev.Name, dev.UDID))
		}
		return "", fmt.Errorf("multiple booted iOS Simulators found (%s); specify --udid=<simulator-udid>", strings.Join(names, ", "))
	}
}

func resolveSimulator(ctx context.Context, xcrunPath, preferredName string) (string, bool, error) {
	if udid, err := autoDetectBootedSimulator(ctx, xcrunPath); err == nil {
		return udid, false, nil
	} else if strings.Contains(err.Error(), "multiple booted") {
		return "", false, err
	}
	udid, err := autoSelectAvailableSimulator(ctx, xcrunPath, preferredName)
	if err != nil {
		return "", false, err
	}
	if err := bootSimulator(ctx, xcrunPath, udid); err != nil {
		return "", false, err
	}
	return udid, true, nil
}

func autoSelectAvailableSimulator(ctx context.Context, xcrunPath, preferredName string) (string, error) {
	out, err := simctl(ctx, xcrunPath, "list", "devices", "available", "-j")
	if err != nil {
		return "", err
	}
	devices, err := parseAvailableSimulators(out, preferredName)
	if err != nil {
		return "", err
	}
	if len(devices) == 0 {
		if preferredName != "" {
			return "", fmt.Errorf("no available iOS Simulator matching %q", preferredName)
		}
		return "", fmt.Errorf("no available iOS Simulator found")
	}
	return devices[0].UDID, nil
}

func parseAvailableSimulators(raw, preferredName string) ([]simctlDevice, error) {
	var list simctlDeviceList
	if err := json.Unmarshal([]byte(raw), &list); err != nil {
		return nil, fmt.Errorf("parse simctl device list: %w", err)
	}
	var available []simctlDevice
	for _, devices := range list.Devices {
		for _, dev := range devices {
			if !dev.IsAvailable {
				continue
			}
			if preferredName != "" && dev.Name != preferredName {
				continue
			}
			available = append(available, dev)
		}
	}
	return available, nil
}

func bootSimulator(ctx context.Context, xcrunPath, udid string) error {
	if _, err := simctl(ctx, xcrunPath, "boot", udid); err != nil {
		if !strings.Contains(err.Error(), "current state: Booted") {
			return fmt.Errorf("boot simulator %q: %w", udid, err)
		}
	}
	return verifyBootedSimulator(ctx, xcrunPath, udid)
}

func verifyBootedSimulator(ctx context.Context, xcrunPath, udid string) error {
	if _, err := simctl(ctx, xcrunPath, "bootstatus", udid, "-b"); err != nil {
		return fmt.Errorf("simulator %q is not booted or not reachable: %w", udid, err)
	}
	return nil
}

func simctl(ctx context.Context, xcrunPath string, args ...string) (string, error) {
	fullArgs := append([]string{"simctl"}, args...)
	cmd := exec.CommandContext(ctx, xcrunPath, fullArgs...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s: %s", err, strings.TrimSpace(string(out)))
	}
	return string(out), nil
}

func normalizeWDAURL(raw string) (string, error) {
	wdaURL := strings.TrimSpace(raw)
	if wdaURL == "" {
		wdaURL = defaultWDAURL
	}
	wdaURL = strings.TrimRight(wdaURL, "/")
	parsed, err := url.Parse(wdaURL)
	if err != nil {
		return "", fmt.Errorf("parse WDA URL: %w", err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("WDA URL must use http or https")
	}
	if !isLoopbackHost(parsed.Hostname()) {
		return "", fmt.Errorf("WDA URL must point to localhost or a loopback address")
	}
	return wdaURL, nil
}

func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func iosCapabilities() []string {
	capabilities := append([]string{}, baseCapabilities...)
	return append(capabilities, uiCapabilities...)
}

func probeWDA(ctx context.Context, client *http.Client, wdaURL string) (bool, string) {
	normalizedURL, err := normalizeWDAURL(wdaURL)
	if err != nil {
		return false, err.Error()
	}

	probeCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, normalizedURL+"/status", nil)
	if err != nil {
		return false, err.Error()
	}
	resp, err := client.Do(req)
	if err != nil {
		return false, err.Error()
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return false, fmt.Sprintf("status endpoint returned HTTP %d", resp.StatusCode)
	}
	return true, "reachable"
}

func (d *Driver) removeArtifactDir(path string) {
	if d.store == nil || path == "" {
		return
	}
	root, err := filepath.Abs(d.store.ArtifactsDir())
	if err != nil {
		return
	}
	target, err := filepath.Abs(path)
	if err != nil {
		return
	}
	rel, err := filepath.Rel(root, target)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return
	}
	_ = os.RemoveAll(target)
}

func requireWDA(session *liveSession) error {
	if session.wdaURL == "" {
		return fmt.Errorf("WebDriverAgent is required for iOS UI actions; expected %s or recreate with --wda-url=<url>", defaultWDAURL)
	}
	return nil
}

func ensureWDASession(ctx context.Context, session *liveSession) (string, error) {
	resp, err := wdaRequest(ctx, session, http.MethodPost, "/session", map[string]any{
		"capabilities": map[string]any{
			"alwaysMatch": map[string]any{},
		},
	})
	if err != nil {
		return "", err
	}
	if sid := stringValue(resp["sessionId"]); sid != "" {
		return sid, nil
	}
	if value, ok := resp["value"].(map[string]any); ok {
		if sid := stringValue(value["sessionId"]); sid != "" {
			return sid, nil
		}
	}
	return "", fmt.Errorf("WDA response did not include sessionId")
}

func findElement(ctx context.Context, session *liveSession, sessionID string, target action.Target) (string, error) {
	using, value, err := locatorPayload(target)
	if err != nil {
		return "", err
	}
	resp, err := wdaRequest(ctx, session, http.MethodPost, "/session/"+sessionID+"/element", map[string]any{
		"using": using,
		"value": value,
	})
	if err != nil {
		return "", err
	}
	elementValue, ok := resp["value"].(map[string]any)
	if !ok {
		return "", fmt.Errorf("WDA response did not include element value")
	}
	for _, key := range []string{"ELEMENT", "element-6066-11e4-a52e-4f735466cecf"} {
		if id := stringValue(elementValue[key]); id != "" {
			return id, nil
		}
	}
	return "", fmt.Errorf("WDA response did not include element id")
}

func locatorPayload(target action.Target) (string, string, error) {
	switch target.Strategy {
	case action.StrategyText:
		escaped := strings.ReplaceAll(target.Value, "'", "\\'")
		return "predicate string", fmt.Sprintf("label == '%s' OR name == '%s' OR value == '%s'", escaped, escaped, escaped), nil
	case action.StrategyID, action.StrategyAcc:
		return "accessibility id", target.Value, nil
	case action.StrategyXPath:
		return "xpath", target.Value, nil
	default:
		return "", "", fmt.Errorf("unsupported target strategy %q for ios; use text:, id:, acc:, xpath:, or xy:", target.Strategy)
	}
}

func wdaRequest(ctx context.Context, session *liveSession, method, path string, payload any) (map[string]any, error) {
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return nil, err
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, session.wdaURL+path, body)
	if err != nil {
		return nil, err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := session.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("WDA %s %s failed: %s", method, path, strings.TrimSpace(string(data)))
	}
	if len(data) == 0 {
		return map[string]any{}, nil
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return nil, fmt.Errorf("parse WDA response: %w", err)
	}
	return out, nil
}

func parsePoint(raw string) (int, int, error) {
	parts := strings.SplitN(raw, ",", 2)
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("point must be in format x,y")
	}
	x, err := strconv.Atoi(strings.TrimSpace(parts[0]))
	if err != nil {
		return 0, 0, fmt.Errorf("invalid x coordinate %q", parts[0])
	}
	y, err := strconv.Atoi(strings.TrimSpace(parts[1]))
	if err != nil {
		return 0, 0, fmt.Errorf("invalid y coordinate %q", parts[1])
	}
	return x, y, nil
}

func normalizeActionName(name string) string {
	return strings.ReplaceAll(strings.ReplaceAll(name, "-", "_"), " ", "_")
}

func stringOption(options map[string]any, key string) string {
	if options == nil {
		return ""
	}
	if v, ok := options[key]; ok {
		return stringValue(v)
	}
	return ""
}

func stringParam(params map[string]any, key string) string {
	if params == nil {
		return ""
	}
	if v, ok := params[key]; ok {
		return stringValue(v)
	}
	return ""
}

func stringValue(v any) string {
	switch val := v.(type) {
	case string:
		return val
	case int:
		return strconv.Itoa(val)
	case float64:
		return strconv.FormatFloat(val, 'f', -1, 64)
	default:
		if v == nil {
			return ""
		}
		return fmt.Sprintf("%v", v)
	}
}

func floatOption(params map[string]any, key string, fallback float64) float64 {
	if params == nil {
		return fallback
	}
	v, ok := params[key]
	if !ok {
		return fallback
	}
	switch val := v.(type) {
	case float64:
		return val
	case int:
		return float64(val)
	case string:
		parsed, err := strconv.ParseFloat(val, 64)
		if err == nil {
			return parsed
		}
	}
	return fallback
}

func invalidParam(message string) action.Result {
	return action.Result{OK: false, Error: &action.Error{Code: "INVALID_ARGUMENT", Message: message}}
}

func runtimeError(code string, err error) action.Result {
	return action.Result{OK: false, Error: &action.Error{Code: code, Message: err.Error()}}
}
