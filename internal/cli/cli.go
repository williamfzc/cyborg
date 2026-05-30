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
		return runHelpCommand(args[1:], stdout, stderr)
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
		return runDoCommand(ctx, client, args[1:], stdout, stderr)
	default:
		_, _ = fmt.Fprintf(stderr, "unsupported command: %s\n", args[0])
		return 1
	}
}

func runHelpCommand(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printHelp(stdout)
		return 0
	}
	// cyborg help <kind> — query daemon for that kind's action list
	kind := device.Kind(args[0])
	client := daemonclient.NewDefault()
	if err := ensureDaemon(client); err != nil {
		// Daemon not available; show static help
		_, _ = fmt.Fprintf(stdout, "cyborg help %s — daemon not available, cannot fetch actions\n", kind)
		return 0
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	actions, err := client.DriverActions(ctx, kind)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "no driver registered for kind %q\n", kind)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "cyborg do — actions for %s devices:\n\n", kind)
	for _, a := range actions {
		_, _ = fmt.Fprintf(stdout, "  %-12s %s\n", a.Name, a.Description)
		for _, p := range a.Params {
			req := ""
			if p.Required {
				req = " (required)"
			}
			_, _ = fmt.Fprintf(stdout, "    --%-10s %s%s\n", p.Name, p.Description, req)
		}
	}
	_, _ = fmt.Fprintln(stdout, "\nIf only one device exists, --device can be omitted.")
	return 0
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

// runDoCommand dispatches an action to a device.
func runDoCommand(ctx context.Context, client *daemonclient.Client, args []string, stdout, stderr io.Writer) int {
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
	_, _ = io.WriteString(w, `cyborg — local device control plane

Usage:
  cyborg up <kind> [options]        Create a device target
  cyborg ls                         List devices (pool status)
  cyborg show <device-id>           Show device details and capabilities
  cyborg rm <device-id>             Remove a device
  cyborg do <action> [flags]        Execute an action on a device

Discovery:
  cyborg help <kind>                Show actions for a device kind
  cyborg version                    Print version

Supported kinds: browser, android, ios (more via drivers)

Targeting elements (--target flag):
  css:<selector>                    CSS selector (browser default)
  text:<visible text>               Text match (android default)
  id:<native-id>                    DOM id / resource-id / accessibility-id
  acc:<label>                       Accessibility label (aria-label / content-desc)
  xy:<x>,<y>                        Screen coordinates

Examples:
  cyborg up browser --headless
  cyborg do open --url=https://example.com
  cyborg do click --target="css:button.submit"
  cyborg do screenshot
  cyborg do click --target="text:Login" --device=android-abc123
  cyborg up ios --udid=<simulator-udid> --wda-url=http://127.0.0.1:8100

If only one device exists, --device can be omitted.

Device reuse (no automatic pooling — caller decides):
  1. cyborg ls                        Check existing devices
  2. If a running device fits → use it directly with cyborg do
  3. If none fits → cyborg up to create a new one

Debug:
  cyborg debug status
  cyborg debug drivers
`)
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
