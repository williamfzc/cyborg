// CLI action dispatch tests for Cyborg.
// They verify major cyborg do actions are serialized correctly without external devices.
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/williamfzc/cyborg/internal/buildinfo"
	"github.com/williamfzc/cyborg/internal/core/action"
	"github.com/williamfzc/cyborg/internal/daemon"
)

func TestExecute_DoDispatchesMajorActions(t *testing.T) {
	received := make(chan action.Action, 64)
	stop := startFakeDaemonForActions(t, received)
	defer stop()

	tests := []struct {
		name        string
		args        []string
		wantAction  string
		wantDevice  string
		wantParams  map[string]any
		wantTimeout int
	}{
		{
			name:       "browser open",
			args:       []string{"do", "open", "--device=browser-1", "--url=https://example.com"},
			wantAction: "open",
			wantDevice: "browser-1",
			wantParams: map[string]any{"url": "https://example.com"},
		},
		{
			name:       "browser click",
			args:       []string{"do", "click", "--device=browser-1", "--target=css:button.submit"},
			wantAction: "click",
			wantDevice: "browser-1",
			wantParams: map[string]any{"target": "css:button.submit"},
		},
		{
			name:       "browser type",
			args:       []string{"do", "type", "--device=browser-1", "--target=css:input[name=email]", "--text=user@test.com"},
			wantAction: "type",
			wantDevice: "browser-1",
			wantParams: map[string]any{"target": "css:input[name=email]", "text": "user@test.com"},
		},
		{
			name:       "browser press",
			args:       []string{"do", "press", "--device=browser-1", "--key=Enter"},
			wantAction: "press",
			wantDevice: "browser-1",
			wantParams: map[string]any{"key": "Enter"},
		},
		{
			name:       "browser eval",
			args:       []string{"do", "eval", "--device=browser-1", "--code=document.title"},
			wantAction: "eval",
			wantDevice: "browser-1",
			wantParams: map[string]any{"code": "document.title"},
		},
		{
			name:       "android shell",
			args:       []string{"do", "shell", "--device=android-1", "--cmd=pm list packages"},
			wantAction: "shell",
			wantDevice: "android-1",
			wantParams: map[string]any{"cmd": "pm list packages"},
		},
		{
			name:       "android install",
			args:       []string{"do", "install", "--device=android-1", "--apk=/tmp/app.apk"},
			wantAction: "install",
			wantDevice: "android-1",
			wantParams: map[string]any{"apk": "/tmp/app.apk"},
		},
		{
			name:       "android swipe",
			args:       []string{"do", "swipe", "--device=android-1", "--from=540,1500", "--to=540,500", "--duration=450"},
			wantAction: "swipe",
			wantDevice: "android-1",
			wantParams: map[string]any{"from": "540,1500", "to": "540,500", "duration": 450},
		},
		{
			name:       "ios launch",
			args:       []string{"do", "launch", "--device=ios-1", "--bundle-id=com.example.app"},
			wantAction: "launch",
			wantDevice: "ios-1",
			wantParams: map[string]any{"bundle_id": "com.example.app"},
		},
		{
			name:       "ios terminate",
			args:       []string{"do", "terminate", "--device=ios-1", "--bundle-id=com.example.app"},
			wantAction: "terminate",
			wantDevice: "ios-1",
			wantParams: map[string]any{"bundle_id": "com.example.app"},
		},
		{
			name:        "ios tree with timeout",
			args:        []string{"do", "tree", "--device=ios-1", "--timeout-ms=3000"},
			wantAction:  "tree",
			wantDevice:  "ios-1",
			wantParams:  map[string]any{},
			wantTimeout: 3000,
		},
		{
			name:       "shared screenshot",
			args:       []string{"do", "screenshot", "--device=ios-1", "--path=/tmp/screen.png"},
			wantAction: "screenshot",
			wantDevice: "ios-1",
			wantParams: map[string]any{"path": "/tmp/screen.png"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := Execute(tt.args, &stdout, &stderr)
			if code != 0 {
				t.Fatalf("expected exit code 0, got %d; stderr=%s stdout=%s", code, stderr.String(), stdout.String())
			}

			got := readAction(t, received)
			if got.Name != tt.wantAction {
				t.Fatalf("expected action %q, got %q", tt.wantAction, got.Name)
			}
			if got.DeviceID != tt.wantDevice {
				t.Fatalf("expected device %q, got %q", tt.wantDevice, got.DeviceID)
			}
			if got.TimeoutMs != tt.wantTimeout {
				t.Fatalf("expected timeout %d, got %d", tt.wantTimeout, got.TimeoutMs)
			}
			assertParams(t, got.Params, tt.wantParams)
		})
	}
}

func startFakeDaemonForActions(t *testing.T, received chan<- action.Action) func() {
	t.Helper()

	listener, err := net.Listen("tcp", daemon.DefaultAddress)
	if err != nil {
		t.Skipf("default daemon port is unavailable: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/status", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		writeTestJSON(t, w, map[string]any{
			"version":  buildinfo.Version,
			"protocol": "test",
			"drivers":  []any{},
			"notes":    []string{"fake daemon for CLI tests"},
		})
	})
	mux.HandleFunc("/actions", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var req action.Action
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode action request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		received <- req
		writeTestJSON(t, w, action.Result{OK: true, Result: map[string]any{"action": req.Name}})
	})
	mux.HandleFunc("/drivers/", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		writeTestJSON(t, w, []map[string]any{
			{"name": "open", "description": "Navigate to a URL"},
			{"name": "click", "description": "Click an element"},
			{"name": "type", "description": "Type text"},
			{"name": "screenshot", "description": "Capture screen"},
			{"name": "shell", "description": "Run shell command"},
			{"name": "install", "description": "Install app"},
			{"name": "launch", "description": "Launch app"},
			{"name": "terminate", "description": "Terminate app"},
			{"name": "tree", "description": "Dump UI tree"},
			{"name": "eval", "description": "Evaluate code"},
		})
	})

	server := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 2 * time.Second,
	}
	done := make(chan struct{})
	go func() {
		_ = server.Serve(listener)
		close(done)
	}()

	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = server.Shutdown(ctx)
		<-done
	}
}

func readAction(t *testing.T, received <-chan action.Action) action.Action {
	t.Helper()
	select {
	case got := <-received:
		return got
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for action request")
		return action.Action{}
	}
}

func assertParams(t *testing.T, got, want map[string]any) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("expected params %v, got %v", want, got)
	}
	for key, wantValue := range want {
		gotValue, ok := got[key]
		if !ok {
			t.Fatalf("expected param %q in %v", key, got)
		}
		if gotValue != wantValue {
			if wantNumber, ok := wantValue.(int); ok {
				if gotNumber, ok := gotValue.(float64); ok && gotNumber == float64(wantNumber) {
					continue
				}
			}
			t.Fatalf("expected param %q=%v (%T), got %v (%T)", key, wantValue, wantValue, gotValue, gotValue)
		}
	}
}

func writeTestJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatalf("encode response: %v", err)
	}
}
