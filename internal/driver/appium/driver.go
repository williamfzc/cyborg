// Appium driver for Cyborg mobile device targets.
// It maps Cyborg actions onto an existing Appium WebDriver server.
package appium

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/williamfzc/cyborg/internal/core/action"
	"github.com/williamfzc/cyborg/internal/core/device"
	coredriver "github.com/williamfzc/cyborg/internal/core/driver"
	"github.com/williamfzc/cyborg/internal/store/localstate"
)

const defaultAppiumURL = "http://127.0.0.1:4723"

type Driver struct {
	kind     device.Kind
	store    *localstate.Store
	mu       sync.Mutex
	sessions map[string]*liveSession
}

type liveSession struct {
	sessionID   string
	appiumURL   string
	artifactDir string
	httpClient  *http.Client
}

func NewAndroid(store *localstate.Store) *Driver {
	return &Driver{
		kind:     device.KindAndroid,
		store:    store,
		sessions: map[string]*liveSession{},
	}
}

func NewIOS(store *localstate.Store) *Driver {
	return &Driver{
		kind:     device.KindIOS,
		store:    store,
		sessions: map[string]*liveSession{},
	}
}

func (d *Driver) Summary() coredriver.Summary {
	switch d.kind {
	case device.KindAndroid:
		return coredriver.Summary{
			Name:    "android-appium",
			Kind:    device.KindAndroid,
			Engine:  "appium",
			Backend: "appium/uiautomator2",
			Capabilities: []string{
				"screenshot", "tree",
			},
			Notes: []string{
				"Connects to an existing Appium server",
				"Uses UiAutomator2 for Android sessions",
				"Use --appium-url to override http://127.0.0.1:4723",
			},
		}
	case device.KindIOS:
		return coredriver.Summary{
			Name:    "ios-appium",
			Kind:    device.KindIOS,
			Engine:  "appium",
			Backend: "appium/xcuitest",
			Capabilities: []string{
				"screenshot", "tree",
			},
			Notes: []string{
				"Connects to an existing Appium server",
				"Uses XCUITest for iOS sessions",
				"Use --appium-url to override http://127.0.0.1:4723",
			},
		}
	default:
		return coredriver.Summary{Name: "appium", Kind: d.kind, Engine: "appium", Backend: "appium"}
	}
}

func (d *Driver) Actions() []coredriver.ActionSpec {
	return []coredriver.ActionSpec{
		{
			Name:        "screenshot",
			Description: "Capture screenshot through Appium",
			Params:      []coredriver.ParamSpec{{Name: "path", Description: "Output file path (auto-generated if omitted)"}},
		},
		{
			Name:        "tree",
			Description: "Return Appium page source",
		},
	}
}

func (d *Driver) Create(ctx context.Context, spec coredriver.CreateSpec) (device.Device, error) {
	appiumURL := normalizeAppiumURL(stringOption(spec.Options, "appium_url"))
	httpClient := &http.Client{Timeout: 10 * time.Minute}

	sessionID, caps, err := d.createSession(ctx, httpClient, appiumURL, spec.Options)
	if err != nil {
		return device.Device{}, err
	}

	now := time.Now()
	artifactDir := filepath.Join(d.store.ArtifactsDir(), fmt.Sprintf("%s-appium-%d", spec.Kind, now.UnixNano()))
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		_ = d.deleteSession(context.Background(), httpClient, appiumURL, sessionID)
		return device.Device{}, fmt.Errorf("create artifact dir: %w", err)
	}

	dev := device.Device{
		ID:           device.NewID(d.kind),
		Kind:         d.kind,
		State:        device.StateRunning,
		Capabilities: []string{"screenshot", "tree"},
		CreatedAt:    now,
		UpdatedAt:    now,
		Metadata: map[string]any{
			"driver":       d.Summary().Name,
			"engine":       "appium",
			"backend":      d.Summary().Backend,
			"appium_url":   appiumURL,
			"session_id":   sessionID,
			"artifact_dir": artifactDir,
			"capabilities": caps,
		},
	}

	d.mu.Lock()
	d.sessions[dev.ID] = &liveSession{
		sessionID:   sessionID,
		appiumURL:   appiumURL,
		artifactDir: artifactDir,
		httpClient:  httpClient,
	}
	d.mu.Unlock()

	return dev, nil
}

func (d *Driver) Destroy(ctx context.Context, dev device.Device) error {
	session, err := d.session(dev)
	if err != nil {
		return nil
	}
	d.mu.Lock()
	delete(d.sessions, dev.ID)
	d.mu.Unlock()
	if err := d.deleteSession(ctx, session.httpClient, session.appiumURL, session.sessionID); err != nil {
		return err
	}
	if session.artifactDir != "" {
		_ = os.RemoveAll(session.artifactDir)
	}
	return nil
}

func (d *Driver) Act(ctx context.Context, dev device.Device, req action.Action) (action.Result, error) {
	session, err := d.session(dev)
	if err != nil {
		return action.Result{OK: false, Error: &action.Error{Code: "DEVICE_UNAVAILABLE", Message: err.Error()}}, nil
	}

	switch normalizeActionName(req.Name) {
	case "screenshot":
		return d.screenshot(ctx, session, stringParam(req.Params, "path")), nil
	case "tree", "source":
		return d.source(ctx, session), nil
	default:
		return action.Result{OK: false, Error: &action.Error{Code: "UNSUPPORTED_ACTION", Message: "unsupported Appium action: " + req.Name}}, nil
	}
}

func (d *Driver) createSession(ctx context.Context, client *http.Client, appiumURL string, options map[string]any) (string, map[string]any, error) {
	caps := d.capabilities(options)
	payload := map[string]any{
		"capabilities": map[string]any{
			"alwaysMatch": caps,
			"firstMatch":  []map[string]any{{}},
		},
	}
	var resp struct {
		Value struct {
			SessionID    string         `json:"sessionId"`
			Capabilities map[string]any `json:"capabilities"`
			Error        string         `json:"error"`
			Message      string         `json:"message"`
		} `json:"value"`
		SessionID string `json:"sessionId"`
	}
	if err := doJSON(ctx, client, http.MethodPost, appiumURL+"/session", payload, &resp); err != nil {
		return "", nil, err
	}
	sessionID := resp.Value.SessionID
	if sessionID == "" {
		sessionID = resp.SessionID
	}
	if sessionID == "" {
		msg := strings.TrimSpace(resp.Value.Message)
		if msg == "" {
			msg = "Appium did not return a session id"
		}
		return "", nil, fmt.Errorf("create Appium session: %s", msg)
	}
	if resp.Value.Capabilities != nil {
		return sessionID, resp.Value.Capabilities, nil
	}
	return sessionID, caps, nil
}

func (d *Driver) capabilities(options map[string]any) map[string]any {
	switch d.kind {
	case device.KindAndroid:
		caps := map[string]any{
			"platformName":                "Android",
			"appium:automationName":       "UiAutomator2",
			"appium:deviceName":           firstNonEmpty(stringOption(options, "device_name"), stringOption(options, "serial"), "Android"),
			"appium:newCommandTimeout":    120,
			"appium:autoGrantPermissions": true,
		}
		copyCap(caps, "appium:udid", stringOption(options, "serial"))
		copyCap(caps, "appium:app", stringOption(options, "app"))
		copyCap(caps, "appium:appPackage", stringOption(options, "app_package"))
		copyCap(caps, "appium:appActivity", stringOption(options, "app_activity"))
		return caps
	case device.KindIOS:
		caps := map[string]any{
			"platformName":                   "iOS",
			"appium:automationName":          "XCUITest",
			"appium:deviceName":              firstNonEmpty(stringOption(options, "device_name"), stringOption(options, "name"), "iPhone"),
			"appium:newCommandTimeout":       120,
			"appium:wdaLaunchTimeout":        240000,
			"appium:wdaConnectionTimeout":    240000,
			"appium:simulatorStartupTimeout": 240000,
		}
		copyCap(caps, "appium:udid", stringOption(options, "udid"))
		copyCap(caps, "appium:app", stringOption(options, "app"))
		copyCap(caps, "appium:bundleId", stringOption(options, "bundle_id"))
		copyCap(caps, "appium:platformVersion", stringOption(options, "platform_version"))
		return caps
	default:
		return map[string]any{"platformName": string(d.kind)}
	}
}

func (d *Driver) screenshot(ctx context.Context, session *liveSession, path string) action.Result {
	var resp struct {
		Value string `json:"value"`
	}
	if err := doJSON(ctx, session.httpClient, http.MethodGet, session.appiumURL+"/session/"+session.sessionID+"/screenshot", nil, &resp); err != nil {
		return runtimeError("SCREENSHOT_FAILED", err)
	}
	content, err := base64.StdEncoding.DecodeString(resp.Value)
	if err != nil {
		return runtimeError("SCREENSHOT_DECODE_FAILED", err)
	}
	if path == "" {
		path = filepath.Join(session.artifactDir, fmt.Sprintf("screenshot-%d.png", time.Now().UnixNano()))
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		return runtimeError("SCREENSHOT_WRITE_FAILED", err)
	}
	return action.Result{
		OK:     true,
		Result: map[string]any{"path": path},
		Artifacts: []action.Artifact{{
			Kind: "screenshot",
			Path: path,
			Name: filepath.Base(path),
		}},
	}
}

func (d *Driver) source(ctx context.Context, session *liveSession) action.Result {
	var resp struct {
		Value string `json:"value"`
	}
	if err := doJSON(ctx, session.httpClient, http.MethodGet, session.appiumURL+"/session/"+session.sessionID+"/source", nil, &resp); err != nil {
		return runtimeError("TREE_FAILED", err)
	}
	return action.Result{OK: true, Result: map[string]any{"source": resp.Value}}
}

func (d *Driver) deleteSession(ctx context.Context, client *http.Client, appiumURL, sessionID string) error {
	if sessionID == "" {
		return nil
	}
	return doJSON(ctx, client, http.MethodDelete, appiumURL+"/session/"+sessionID, nil, nil)
}

func (d *Driver) session(dev device.Device) (*liveSession, error) {
	d.mu.Lock()
	session, ok := d.sessions[dev.ID]
	d.mu.Unlock()
	if ok {
		return session, nil
	}
	sessionID := stringMetadata(dev.Metadata, "session_id")
	if sessionID == "" {
		return nil, fmt.Errorf("Appium session is not available")
	}
	return &liveSession{
		sessionID:   sessionID,
		appiumURL:   normalizeAppiumURL(stringMetadata(dev.Metadata, "appium_url")),
		artifactDir: stringMetadata(dev.Metadata, "artifact_dir"),
		httpClient:  &http.Client{Timeout: 10 * time.Minute},
	}, nil
}

func doJSON(ctx context.Context, client *http.Client, method, url string, payload any, out any) error {
	var body io.Reader
	if payload != nil {
		content, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		body = bytes.NewReader(content)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return err
	}
	if payload != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	content, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("Appium %s %s failed: status %d: %s", method, url, resp.StatusCode, strings.TrimSpace(string(content)))
	}
	if out == nil || len(content) == 0 {
		return nil
	}
	if err := json.Unmarshal(content, out); err != nil {
		return fmt.Errorf("decode Appium response: %w", err)
	}
	return nil
}

func runtimeError(code string, err error) action.Result {
	return action.Result{OK: false, Error: &action.Error{Code: code, Message: err.Error(), Retryable: true}}
}

func normalizeActionName(name string) string {
	return strings.ToLower(strings.TrimSpace(strings.ReplaceAll(name, "-", "_")))
}

func normalizeAppiumURL(raw string) string {
	raw = strings.TrimRight(strings.TrimSpace(raw), "/")
	if raw == "" {
		return defaultAppiumURL
	}
	return raw
}

func copyCap(caps map[string]any, key, value string) {
	if value != "" {
		caps[key] = value
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func stringOption(options map[string]any, key string) string {
	if options == nil {
		return ""
	}
	value, ok := options[key]
	if !ok {
		return ""
	}
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

func stringParam(params map[string]any, key string) string {
	if params == nil {
		return ""
	}
	value, ok := params[key]
	if !ok {
		return ""
	}
	text, _ := value.(string)
	return strings.TrimSpace(text)
}

func stringMetadata(metadata map[string]any, key string) string {
	if metadata == nil {
		return ""
	}
	value, ok := metadata[key]
	if !ok {
		return ""
	}
	text, _ := value.(string)
	return strings.TrimSpace(text)
}
