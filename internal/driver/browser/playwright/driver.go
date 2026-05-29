// Chromium browser driver for Cyborg.
// It keeps a daemon-owned browser session for repeated actions.
package playwright

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/chromedp"
	"github.com/williamfzc/cyborg/internal/core/action"
	"github.com/williamfzc/cyborg/internal/core/device"
	coredriver "github.com/williamfzc/cyborg/internal/core/driver"
	"github.com/williamfzc/cyborg/internal/store/localstate"
)

type liveSession struct {
	ctx         context.Context
	allocCancel context.CancelFunc
	browserStop context.CancelFunc
	userDataDir string
	artifactDir string
	mu          sync.Mutex
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
		Name:    "browser-playwright",
		Kind:    device.KindBrowser,
		Backend: "chromium/cdp",
		Capabilities: []string{
			"open", "click", "type", "press", "screenshot", "eval",
		},
		Notes: []string{
			"v0 focuses on Chromium-compatible browsers only",
			"the daemon owns browser contexts for repeated agent actions",
			"target strategies: css (default), id, acc",
		},
	}
}

func (d *Driver) Actions() []coredriver.ActionSpec {
	return []coredriver.ActionSpec{
		{
			Name:        "open",
			Description: "Navigate to a URL",
			Params:      []coredriver.ParamSpec{{Name: "url", Description: "URL to navigate to", Required: true}},
		},
		{
			Name:        "click",
			Description: "Click an element",
			Params:      []coredriver.ParamSpec{{Name: "target", Description: "Element locator (css:sel | id:id | acc:label)", Required: true}},
		},
		{
			Name:        "type",
			Description: "Type text into an element",
			Params: []coredriver.ParamSpec{
				{Name: "target", Description: "Element locator", Required: true},
				{Name: "text", Description: "Text to type", Required: true},
			},
		},
		{
			Name:        "press",
			Description: "Press a keyboard key",
			Params:      []coredriver.ParamSpec{{Name: "key", Description: "Key name (Enter, Tab, Escape, etc.)", Required: true}},
		},
		{
			Name:        "screenshot",
			Description: "Capture viewport screenshot",
			Params:      []coredriver.ParamSpec{{Name: "path", Description: "Output file path (auto-generated if omitted)"}},
		},
		{
			Name:        "eval",
			Description: "Evaluate JavaScript in page context",
			Params:      []coredriver.ParamSpec{{Name: "code", Description: "JavaScript expression", Required: true}},
		},
	}
}

func (d *Driver) Create(ctx context.Context, spec coredriver.CreateSpec) (device.Device, error) {
	browserPath, err := findBrowserExecutable()
	if err != nil {
		return device.Device{}, err
	}
	headless := boolOption(spec.Options, "headless", true)
	now := time.Now()
	artifactDir := filepath.Join(d.store.ArtifactsDir(), fmt.Sprintf("%s-%d", spec.Kind, now.UnixNano()))
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		return device.Device{}, fmt.Errorf("create artifact dir: %w", err)
	}
	userDataDir, err := os.MkdirTemp("", "cyborg-browser-*")
	if err != nil {
		return device.Device{}, fmt.Errorf("create browser profile dir: %w", err)
	}

	allocOptions := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.ExecPath(browserPath),
		chromedp.UserDataDir(userDataDir),
		chromedp.NoDefaultBrowserCheck,
		chromedp.NoFirstRun,
		chromedp.Flag("headless", headless),
		chromedp.Flag("disable-gpu", true),
		chromedp.Flag("disable-background-networking", true),
		chromedp.Flag("disable-sync", true),
	)

	allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(), allocOptions...)
	browserCtx, browserStop := chromedp.NewContext(allocCtx)
	if err := chromedp.Run(browserCtx); err != nil {
		allocCancel()
		browserStop()
		_ = os.RemoveAll(userDataDir)
		return device.Device{}, fmt.Errorf("start browser runtime: %w", err)
	}

	dev := device.Device{
		ID:        device.NewID(device.KindBrowser),
		Kind:      device.KindBrowser,
		State:     device.StateRunning,
		CreatedAt: now,
		UpdatedAt: now,
		Capabilities: []string{
			"open", "click", "type", "press", "screenshot", "eval",
		},
		Metadata: map[string]any{
			"driver":          "browser-playwright",
			"backend":         "chromium/cdp",
			"browser_path":    browserPath,
			"browser_engine":  "chromium",
			"headless":        headless,
			"artifact_dir":    artifactDir,
			"user_data_dir":   userDataDir,
			"execution_model": "daemon-owned",
		},
	}

	d.mu.Lock()
	d.sessions[dev.ID] = &liveSession{
		ctx:         browserCtx,
		allocCancel: allocCancel,
		browserStop: browserStop,
		userDataDir: userDataDir,
		artifactDir: artifactDir,
	}
	d.mu.Unlock()

	if initialURL := stringOption(spec.Options, "url"); initialURL != "" {
		result, err := d.Act(ctx, dev, action.Action{
			DeviceID: dev.ID,
			Name:     "open",
			Params: map[string]any{
				"url": initialURL,
			},
		})
		if err != nil {
			_ = d.Destroy(context.Background(), dev)
			return device.Device{}, err
		}
		if !result.OK {
			_ = d.Destroy(context.Background(), dev)
			return device.Device{}, fmt.Errorf("open initial url failed: %s", result.Error.Message)
		}
	}

	return dev, nil
}

func (d *Driver) Destroy(_ context.Context, dev device.Device) error {
	d.mu.Lock()
	session, ok := d.sessions[dev.ID]
	if ok {
		delete(d.sessions, dev.ID)
	}
	d.mu.Unlock()
	if !ok {
		if userDataDir, ok := dev.Metadata["user_data_dir"].(string); ok && userDataDir != "" {
			_ = os.RemoveAll(userDataDir)
		}
		if artifactDir, ok := dev.Metadata["artifact_dir"].(string); ok && artifactDir != "" {
			_ = os.RemoveAll(artifactDir)
		}
		return nil
	}
	session.browserStop()
	session.allocCancel()
	_ = os.RemoveAll(session.userDataDir)
	_ = os.RemoveAll(session.artifactDir)
	return nil
}

func (d *Driver) Act(_ context.Context, dev device.Device, req action.Action) (action.Result, error) {
	session, err := d.session(dev.ID)
	if err != nil {
		return action.Result{OK: false, Error: &action.Error{Code: "DEVICE_UNAVAILABLE", Message: err.Error()}}, nil
	}

	session.mu.Lock()
	defer session.mu.Unlock()

	runCtx := session.ctx
	var cancel context.CancelFunc
	if req.TimeoutMs > 0 {
		runCtx, cancel = context.WithTimeout(session.ctx, time.Duration(req.TimeoutMs)*time.Millisecond)
		defer cancel()
	}

	switch normalizeActionName(req.Name) {
	case "open", "open_url":
		url := stringParam(req.Params, "url")
		if url == "" {
			return invalidParam("missing --url"), nil
		}
		if err := chromedp.Run(runCtx,
			chromedp.Navigate(url),
			chromedp.Sleep(700*time.Millisecond),
		); err != nil {
			return runtimeError("OPEN_FAILED", err), nil
		}
		return action.Result{OK: true, Result: map[string]any{"url": url}}, nil
	case "click":
		target := stringParam(req.Params, "target")
		if target == "" {
			// Fallback: accept legacy --selector param
			target = stringParam(req.Params, "selector")
		}
		if target == "" {
			return invalidParam("missing --target"), nil
		}
		selector, err := d.resolveSelector(target)
		if err != nil {
			return invalidParam(err.Error()), nil
		}
		if err := chromedp.Run(runCtx,
			chromedp.WaitVisible(selector, chromedp.ByQuery),
			chromedp.Click(selector, chromedp.ByQuery),
		); err != nil {
			return runtimeError("CLICK_FAILED", err), nil
		}
		return action.Result{OK: true, Result: map[string]any{"target": target}}, nil
	case "type":
		target := stringParam(req.Params, "target")
		if target == "" {
			target = stringParam(req.Params, "selector")
		}
		text := stringParam(req.Params, "text")
		if target == "" {
			return invalidParam("missing --target"), nil
		}
		selector, err := d.resolveSelector(target)
		if err != nil {
			return invalidParam(err.Error()), nil
		}
		if err := chromedp.Run(runCtx,
			chromedp.WaitVisible(selector, chromedp.ByQuery),
			chromedp.Focus(selector, chromedp.ByQuery),
			chromedp.SetValue(selector, text, chromedp.ByQuery),
		); err != nil {
			return runtimeError("TYPE_FAILED", err), nil
		}
		return action.Result{OK: true, Result: map[string]any{"text": text}}, nil
	case "press":
		key := stringParam(req.Params, "key")
		if key == "" {
			return invalidParam("missing --key"), nil
		}
		if err := chromedp.Run(runCtx, chromedp.KeyEvent(key)); err != nil {
			return runtimeError("PRESS_FAILED", err), nil
		}
		return action.Result{OK: true, Result: map[string]any{"key": key}}, nil
	case "screenshot":
		path := stringParam(req.Params, "path")
		if path == "" {
			path = filepath.Join(session.artifactDir, fmt.Sprintf("screenshot-%d.png", time.Now().UnixNano()))
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return runtimeError("ARTIFACT_WRITE_FAILED", err), nil
		}
		var data []byte
		if err := chromedp.Run(runCtx, chromedp.CaptureScreenshot(&data)); err != nil {
			return runtimeError("SCREENSHOT_FAILED", err), nil
		}
		if err := os.WriteFile(path, data, 0o644); err != nil {
			return runtimeError("ARTIFACT_WRITE_FAILED", err), nil
		}
		artifact := action.Artifact{Kind: "screenshot", Path: path, Name: filepath.Base(path)}
		return action.Result{OK: true, Artifacts: []action.Artifact{artifact}, Result: map[string]any{"path": path}}, nil
	case "eval", "eval_js":
		code := stringParam(req.Params, "code")
		if code == "" {
			return invalidParam("missing --code"), nil
		}
		var result any
		if err := chromedp.Run(runCtx, chromedp.Evaluate(code, &result)); err != nil {
			return runtimeError("EVAL_FAILED", err), nil
		}
		return action.Result{OK: true, Result: map[string]any{"value": result}}, nil
	default:
		return action.Result{OK: false, Error: &action.Error{Code: "ACTION_UNSUPPORTED", Message: fmt.Sprintf("unsupported action %q for browser device", req.Name)}}, nil
	}
}

func (d *Driver) session(deviceID string) (*liveSession, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	session, ok := d.sessions[deviceID]
	if !ok {
		return nil, errors.New("device session is not live in daemon; please recreate the device with `cyborg up browser`")
	}
	return session, nil
}

func findBrowserExecutable() (string, error) {
	candidates := []string{
		"/Applications/Google Chrome.app/Contents/MacOS/Google Chrome",
		"/Applications/Chromium.app/Contents/MacOS/Chromium",
		"/Applications/Microsoft Edge.app/Contents/MacOS/Microsoft Edge",
		"google-chrome",
		"chromium",
		"chromium-browser",
		"microsoft-edge",
	}
	for _, candidate := range candidates {
		if filepath.IsAbs(candidate) {
			if _, err := os.Stat(candidate); err == nil {
				return candidate, nil
			}
			continue
		}
		if path, err := execLookPath(candidate); err == nil {
			return path, nil
		}
	}
	return "", fmt.Errorf("no Chromium-compatible browser found; tried Chrome/Chromium/Edge")
}

func execLookPath(name string) (string, error) {
	return exec.LookPath(name)
}

func boolOption(options map[string]any, key string, fallback bool) bool {
	if options == nil {
		return fallback
	}
	v, ok := options[key]
	if !ok {
		return fallback
	}
	b, ok := v.(bool)
	if ok {
		return b
	}
	return fallback
}

func stringOption(options map[string]any, key string) string {
	if options == nil {
		return ""
	}
	return stringParam(options, key)
}

func stringParam(params map[string]any, key string) string {
	if params == nil {
		return ""
	}
	if v, ok := params[key]; ok {
		s, _ := v.(string)
		return s
	}
	return ""
}

func normalizeActionName(name string) string {
	if mapped, ok := map[string]string{
		"open-url": "open_url",
		"eval-js":  "eval_js",
	}[name]; ok {
		return mapped
	}
	return strings.ReplaceAll(name, "-", "_")
}

// resolveSelector converts a target string (with strategy prefix) into a CSS query selector.
// For the browser, css is the default strategy.
func (d *Driver) resolveSelector(raw string) (string, error) {
	t := action.ParseTarget(raw, action.StrategyCSS)
	switch t.Strategy {
	case action.StrategyCSS:
		return t.Value, nil
	case action.StrategyID:
		return "#" + t.Value, nil
	case action.StrategyXPath:
		// chromedp ByQuery doesn't support xpath; return error for now
		return "", fmt.Errorf("xpath strategy not yet supported for browser; use css: instead")
	case action.StrategyText:
		// Use a broad text-content selector via xpath would require BySearch
		// For now, treat as :has-text pseudo (not standard CSS) — use XPath-like approach
		return "", fmt.Errorf("text strategy not yet supported for browser click; use css: instead")
	case action.StrategyAcc:
		return fmt.Sprintf("[aria-label=%q]", t.Value), nil
	case action.StrategyXY:
		return "", fmt.Errorf("xy strategy not supported for browser; use css: or id: instead")
	default:
		return "", fmt.Errorf("unsupported target strategy %q for browser", t.Strategy)
	}
}

func invalidParam(message string) action.Result {
	return action.Result{OK: false, Error: &action.Error{Code: "INVALID_ARGUMENT", Message: message}}
}

func runtimeError(code string, err error) action.Result {
	return action.Result{OK: false, Error: &action.Error{Code: code, Message: err.Error()}}
}
