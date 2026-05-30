// Unit tests for the Cyborg CLI parser and help output.
// They cover behavior that does not require a live daemon.
package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestMain(m *testing.M) {
	home, err := os.MkdirTemp("", "cyborg-cli-test-home-*")
	if err != nil {
		panic(err)
	}
	originalHome := os.Getenv("HOME")
	if os.Getenv("ANDROID_AVD_HOME") == "" && originalHome != "" {
		avdHome := filepath.Join(originalHome, ".android", "avd")
		if info, err := os.Stat(avdHome); err == nil && info.IsDir() {
			_ = os.Setenv("ANDROID_AVD_HOME", avdHome)
		}
	}
	if err := os.Setenv("HOME", home); err != nil {
		panic(err)
	}
	code := m.Run()
	if originalHome != "" {
		_ = os.Setenv("HOME", originalHome)
	}
	_ = os.RemoveAll(home)
	os.Exit(code)
}

func TestExecute_HelpFlags(t *testing.T) {
	tests := []struct {
		name string
		args []string
	}{
		{"help", []string{"help"}},
		{"-h", []string{"-h"}},
		{"--help", []string{"--help"}},
		{"no args", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := Execute(tt.args, &stdout, &stderr)
			if code != 0 {
				t.Fatalf("expected exit code 0, got %d", code)
			}
			if !strings.Contains(stdout.String(), "cyborg") {
				t.Fatalf("expected help output to contain 'cyborg', got: %s", stdout.String())
			}
			if stderr.Len() != 0 {
				t.Fatalf("expected no stderr output, got: %s", stderr.String())
			}
		})
	}
}

func TestExecute_HelpListsSupportedDeviceKinds(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Execute([]string{"help"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got: %s", stderr.String())
	}

	output := stdout.String()
	for _, want := range []string{
		"Supported kinds: browser, android, ios",
		"Treat this help as the CLI reference, not as a complete operating guide.",
		"Before real work, write a project-specific skill for the user.",
		"When using a device kind, run cyborg help <kind> first.",
		"cyborg up browser --headless",
		"cyborg do click --target=\"text:Login\" --device=android-abc123",
		"cyborg up ios --udid=<simulator-udid> --wda-url=http://127.0.0.1:8100",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("expected help output to contain %q, got: %s", want, output)
		}
	}
}

func TestExecute_Version(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Execute([]string{"version"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("expected exit code 0, got %d", code)
	}
	got := strings.TrimSpace(stdout.String())
	if got != version {
		t.Fatalf("expected version %q, got %q", version, got)
	}
	if stderr.Len() != 0 {
		t.Fatalf("expected no stderr output, got: %s", stderr.String())
	}
}

func TestExecute_ShowWithoutID(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Execute([]string{"show"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("expected exit code 1, got %d", code)
	}
	if !strings.Contains(stderr.String(), "missing device id") {
		t.Fatalf("expected stderr to contain 'missing device id', got: %s", stderr.String())
	}
}

func TestExecute_RmWithoutID(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Execute([]string{"rm"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("expected exit code 1, got %d", code)
	}
	if !strings.Contains(stderr.String(), "missing device id") {
		t.Fatalf("expected stderr to contain 'missing device id', got: %s", stderr.String())
	}
}

func TestExecute_DoWithoutAction(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Execute([]string{"do"}, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("expected exit code 1, got %d", code)
	}
	if !strings.Contains(stderr.String(), "missing action name") {
		t.Fatalf("expected stderr to contain 'missing action name', got: %s", stderr.String())
	}
}

func TestExecute_UnsupportedCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Execute([]string{"browser"}, &stdout, &stderr)
	// "browser" is no longer a top-level command; should fail
	if code != 1 {
		t.Fatalf("expected exit code 1, got %d (stdout=%s)", code, stdout.String())
	}
}

func TestParseKVFlags(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		wantKey   string
		wantValue any
		wantErr   bool
	}{
		{
			name:      "key=value string",
			args:      []string{"--key=value"},
			wantKey:   "key",
			wantValue: "value",
		},
		{
			name:      "boolean flag without value",
			args:      []string{"--flag"},
			wantKey:   "flag",
			wantValue: true,
		},
		{
			name:      "int coercion",
			args:      []string{"--timeout-ms=5000"},
			wantKey:   "timeout_ms",
			wantValue: 5000,
		},
		{
			name:      "bool coercion false",
			args:      []string{"--headless=false"},
			wantKey:   "headless",
			wantValue: false,
		},
		{
			name:      "dash to underscore",
			args:      []string{"--some-flag=hello"},
			wantKey:   "some_flag",
			wantValue: "hello",
		},
		{
			name:    "positional arg error",
			args:    []string{"positional"},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := parseKVFlags(tt.args)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			got, ok := result[tt.wantKey]
			if !ok {
				t.Fatalf("expected key %q in result, got: %v", tt.wantKey, result)
			}
			if got != tt.wantValue {
				t.Fatalf("for key %q: expected %v (%T), got %v (%T)", tt.wantKey, tt.wantValue, tt.wantValue, got, got)
			}
		})
	}
}
