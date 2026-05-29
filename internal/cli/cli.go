// Command-line surface for Cyborg.
// It translates stateless CLI calls into daemon API requests.
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/williamfzc/cyborg/internal/buildinfo"
	"github.com/williamfzc/cyborg/internal/core/action"
	"github.com/williamfzc/cyborg/internal/core/device"
	"github.com/williamfzc/cyborg/internal/daemon"
	daemonclient "github.com/williamfzc/cyborg/internal/daemon/client"
)

const version = buildinfo.Version

func Execute(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printHelp(stdout)
		return 0
	}

	switch args[0] {
	case "help", "-h", "--help":
		printHelp(stdout)
		return 0
	case "version":
		_, _ = fmt.Fprintf(stdout, "%s\n", version)
		return 0
	case "daemon":
		return runDaemonCommand(args[1:], stdout, stderr)
	}

	// Early validation that doesn't need a daemon connection
	switch args[0] {
	case "show":
		if len(args) < 2 {
			_, _ = fmt.Fprintln(stderr, "missing device id")
			return 1
		}
	case "rm":
		if len(args) < 2 {
			_, _ = fmt.Fprintln(stderr, "missing device id")
			return 1
		}
	case "do":
		if len(args) < 2 {
			_, _ = fmt.Fprintln(stderr, "missing action name")
			return 1
		}
	case "browser":
		if len(args) < 2 {
			printKindHelp(device.KindBrowser, stdout)
			return 0
		}
	case "android":
		if len(args) < 2 {
			printKindHelp(device.KindAndroid, stdout)
			return 0
		}
	case "up":
		if len(args) < 2 {
			_, _ = fmt.Fprintln(stderr, "missing device kind")
			return 0
		}
	}

	client := daemonclient.NewDefault()
	if err := ensureDaemon(client); err != nil {
		_, _ = fmt.Fprintf(stderr, "start daemon failed: %v\n", err)
		return 1
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	switch args[0] {
	case "debug":
		return runDebugCommand(ctx, client, args[1:], stdout, stderr)
	case "up":
		return runUpCommand(ctx, client, args[1:], stdout, stderr)
	case "ls":
		devices, err := client.List(ctx)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "list devices failed: %v\n", err)
			return 1
		}
		return writeJSON(stdout, devices)
	case "show":
		dev, err := client.Show(ctx, args[1])
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "show device failed: %v\n", err)
			return 1
		}
		return writeJSON(stdout, dev)
	case "rm":
		if err := client.Remove(ctx, args[1]); err != nil {
			_, _ = fmt.Fprintf(stderr, "remove device failed: %v\n", err)
			return 1
		}
		return writeJSON(stdout, map[string]any{"ok": true, "device_id": args[1]})
	case "do":
		return runDoCommand(ctx, client, args[1:], "", stdout, stderr)
	case "browser":
		return runKindCommand(ctx, client, device.KindBrowser, args[1:], stdout, stderr)
	case "android":
		return runKindCommand(ctx, client, device.KindAndroid, args[1:], stdout, stderr)
	default:
		_, _ = fmt.Fprintf(stderr, "unsupported command: %s\n", args[0])
		return 1
	}
}

func runDebugCommand(ctx context.Context, client *daemonclient.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, "missing debug subcommand")
		return 1
	}
	switch args[0] {
	case "status":
		status, err := client.Status(ctx)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "get status failed: %v\n", err)
			return 1
		}
		return writeJSON(stdout, status)
	case "drivers":
		drivers, err := client.Drivers(ctx)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "get drivers failed: %v\n", err)
			return 1
		}
		return writeJSON(stdout, drivers)
	default:
		_, _ = fmt.Fprintf(stderr, "unsupported debug subcommand: %s\n", args[0])
		return 1
	}
}

func runUpCommand(ctx context.Context, client *daemonclient.Client, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, "missing device kind")
		return 1
	}
	options, err := parseKVFlags(args[1:])
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "parse options failed: %v\n", err)
		return 1
	}
	dev, err := client.Create(ctx, device.Kind(args[0]), options)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "create device failed: %v\n", err)
		return 1
	}
	return writeJSON(stdout, dev)
}

// runDoCommand dispatches an action to the specified device.
// If expectKind is non-empty, the target device must match that kind.
func runDoCommand(ctx context.Context, client *daemonclient.Client, args []string, expectKind device.Kind, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, "missing action name")
		return 1
	}
	actionName := args[0]
	params, err := parseKVFlags(args[1:])
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "parse action params failed: %v\n", err)
		return 1
	}

	req := action.Action{
		Name:   actionName,
		Params: params,
	}
	if deviceID, ok := params["device"].(string); ok && deviceID != "" {
		req.DeviceID = deviceID
		delete(req.Params, "device")
	}
	if timeoutMs, ok := params["timeout_ms"].(int); ok {
		req.TimeoutMs = timeoutMs
		delete(req.Params, "timeout_ms")
	}

	// Validate device kind if required
	if expectKind != "" {
		var targetDev device.Device
		if req.DeviceID != "" {
			dev, err := client.Show(ctx, req.DeviceID)
			if err != nil {
				_, _ = fmt.Fprintf(stderr, "get device failed: %v\n", err)
				return 1
			}
			targetDev = dev
		} else {
			// Auto-resolve: list all and pick the single one
			devices, err := client.List(ctx)
			if err != nil {
				_, _ = fmt.Fprintf(stderr, "list devices failed: %v\n", err)
				return 1
			}
			switch len(devices) {
			case 0:
				_, _ = fmt.Fprintln(stderr, "no devices available; create one with 'cyborg up'")
				return 1
			case 1:
				targetDev = devices[0]
			default:
				_, _ = fmt.Fprintln(stderr, "multiple devices exist; specify --device=<id>")
				return 1
			}
		}
		if targetDev.Kind != expectKind {
			_, _ = fmt.Fprintf(stderr, "device %q is %q, but this command requires a %q device\n", targetDev.ID, targetDev.Kind, expectKind)
			return 1
		}
	}

	result, err := client.Act(ctx, req)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "execute action failed: %v\n", err)
		return 1
	}
	if !result.OK && result.Error != nil {
		_ = writeJSON(stdout, result)
		return 2
	}
	return writeJSON(stdout, result)
}

// runKindCommand handles `device <kind> <action>` — device-specific sub-commands.
func runKindCommand(ctx context.Context, client *daemonclient.Client, kind device.Kind, args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printKindHelp(kind, stdout)
		return 0
	}
	return runDoCommand(ctx, client, args, kind, stdout, stderr)
}

func runDaemonCommand(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		_, _ = fmt.Fprintln(stderr, "missing daemon subcommand")
		return 1
	}
	srv, err := daemon.NewDefaultServer()
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "init daemon failed: %v\n", err)
		return 1
	}
	switch args[0] {
	case "serve":
		ctx := context.Background()
		if err := srv.ListenAndServe(ctx, daemon.DefaultAddress); err != nil {
			_, _ = fmt.Fprintf(stderr, "daemon serve failed: %v\n", err)
			return 1
		}
		return 0
	case "status":
		return writeJSON(stdout, srv.Status())
	default:
		_, _ = fmt.Fprintf(stderr, "unsupported daemon subcommand: %s\n", args[0])
		return 1
	}
}

func ensureDaemon(client *daemonclient.Client) error {
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	status, err := client.Status(ctx)
	cancel()
	if err == nil {
		if status.Version == version {
			return nil
		}
		_ = stopManagedDaemon()
		_ = stopListenerOnDefaultPort()
	}

	baseDir, err := daemon.BaseDir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(baseDir, 0o755); err != nil {
		return err
	}
	logPath, err := daemon.LogFilePath()
	if err != nil {
		return err
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	cmd := exec.Command(os.Args[0], "daemon", "serve")
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return err
	}
	go func() {
		_ = cmd.Wait()
		_ = logFile.Close()
	}()

	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
		status, err := client.Status(ctx)
		cancel()
		if err == nil && status.Version == version {
			return nil
		}
		time.Sleep(250 * time.Millisecond)
	}
	return fmt.Errorf("daemon did not become healthy at %s", daemon.DefaultBaseURL())
}

func stopManagedDaemon() error {
	pidPath, err := daemon.PIDFilePath()
	if err != nil {
		return err
	}
	content, err := os.ReadFile(pidPath)
	if err != nil {
		return nil
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(content)))
	if err != nil {
		return nil
	}
	if err := syscall.Kill(pid, syscall.SIGTERM); err != nil {
		return err
	}
	time.Sleep(300 * time.Millisecond)
	_ = os.Remove(pidPath)
	return nil
}

func stopListenerOnDefaultPort() error {
	cmd := exec.Command("lsof", "-ti", "tcp:58583")
	output, err := cmd.Output()
	if err != nil {
		return nil
	}
	for _, line := range strings.Split(strings.TrimSpace(string(output)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		pid, err := strconv.Atoi(strings.TrimSpace(line))
		if err != nil {
			continue
		}
		_ = syscall.Kill(pid, syscall.SIGTERM)
	}
	time.Sleep(300 * time.Millisecond)
	return nil
}

func printHelp(w io.Writer) {
	_, _ = io.WriteString(w, `cyborg

Usage:
  cyborg version
  cyborg up <browser|android> [options]
  cyborg ls
  cyborg show <device-id>
  cyborg rm <device-id>

Universal actions (work on any device):
  cyborg do click --device=<id> --selector=<sel>
  cyborg do type --device=<id> --selector=<sel> --text=<text>
  cyborg do press --device=<id> --key=<key>
  cyborg do screenshot --device=<id> [--path=<file>]

Device-specific commands (hierarchical):
  cyborg browser <action> --device=<id> [flags]
  cyborg android <action> --device=<id> [flags]

If only one device exists, --device can be omitted.

Examples:
  cyborg up browser --headless=true
  cyborg do click --device=browser-abc123 --selector='button'
  cyborg do screenshot
  cyborg browser open --url=https://example.com
  cyborg browser eval --code='document.title'

Run 'cyborg browser' or 'cyborg android' for device-specific help.

Debug:
  cyborg debug status
  cyborg debug drivers
`)
}

func printKindHelp(kind device.Kind, w io.Writer) {
	switch kind {
	case device.KindBrowser:
		_, _ = io.WriteString(w, `cyborg browser — browser-specific commands

Usage:
  cyborg browser <action> --device=<id> [flags]

Actions:
  open    --url=<url>           Navigate to a URL
  eval    --code=<js>           Evaluate JavaScript in page context

If only one device exists, --device can be omitted.
Use 'cyborg do' for universal actions (click, type, press, screenshot).
`)
	case device.KindAndroid:
		_, _ = io.WriteString(w, `cyborg android — android-specific commands

Usage:
  cyborg android <action> --device=<id> [flags]

Actions:
  tree                           Dump UI hierarchy
  shell   --cmd=<command>        Run adb shell command
  install --apk=<path>           Install an APK
  swipe   --from=<x,y> --to=<x,y>  Swipe gesture
  observe                        Observe screen state

If only one device exists, --device can be omitted.
Use 'cyborg do' for universal actions (click, type, press, screenshot).
`)
	default:
		_, _ = fmt.Fprintf(w, "no help available for device kind %q\n", kind)
	}
}

func writeJSON(w io.Writer, v any) int {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return 1
	}
	return 0
}

func parseKVFlags(args []string) (map[string]any, error) {
	params := map[string]any{}
	for _, arg := range args {
		if !strings.HasPrefix(arg, "--") {
			return nil, fmt.Errorf("unsupported positional argument %q", arg)
		}
		kv := strings.TrimPrefix(arg, "--")
		parts := strings.SplitN(kv, "=", 2)
		key := normalizeFlagKey(parts[0])
		value := "true"
		if len(parts) == 2 {
			value = parts[1]
		}
		params[key] = coerceValue(value)
	}
	return params, nil
}

func normalizeFlagKey(key string) string {
	return strings.ReplaceAll(strings.TrimSpace(key), "-", "_")
}

func coerceValue(value string) any {
	if b, err := strconv.ParseBool(value); err == nil {
		return b
	}
	if i, err := strconv.Atoi(value); err == nil {
		return i
	}
	return value
}
