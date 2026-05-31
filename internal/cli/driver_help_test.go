// CLI tests for dynamically discovered driver help.
// They verify each supported device kind is visible through the public CLI.
package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestExecute_DriverHelpForSupportedKinds(t *testing.T) {
	stopDaemon := ensureTestDaemon(t)
	defer stopDaemon()

	tests := []struct {
		kind string
		want []string
	}{
		{
			kind: "browser",
			want: []string{
				"cyborg do — actions for browser devices:",
				"open",
				"eval",
				"Usage:",
				"cyborg do <action> [--device=<id>] [--timeout-ms=<ms>] [flags]",
				"Run cyborg ls first.",
				"If no running browser device fits, create one with cyborg up browser.",
				"Command output is JSON. If ok is false, read error before retrying.",
			},
		},
		{
			kind: "android",
			want: []string{
				"cyborg do — actions for android devices:",
				"shell",
				"install",
				"Usage:",
				"cyborg do <action> [--device=<id>] [--timeout-ms=<ms>] [flags]",
				"Run cyborg ls first.",
				"If no running android device fits, create one with cyborg up android.",
				"Command output is JSON. If ok is false, read error before retrying.",
			},
		},
		{
			kind: "ios",
			want: []string{
				"cyborg do — actions for ios devices:",
				"launch",
				"terminate",
				"tree",
				"Usage:",
				"cyborg do <action> [--device=<id>] [--timeout-ms=<ms>] [flags]",
				"Run cyborg ls first.",
				"If no running ios device fits, create one with cyborg up ios.",
				"Command output is JSON. If ok is false, read error before retrying.",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.kind, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := Execute([]string{"help", tt.kind}, &stdout, &stderr)
			if code != 0 {
				t.Fatalf("help %s failed (code=%d): %s", tt.kind, code, stderr.String())
			}
			if stderr.Len() != 0 {
				t.Fatalf("expected no stderr output, got: %s", stderr.String())
			}
			output := stdout.String()
			for _, want := range tt.want {
				if !strings.Contains(output, want) {
					t.Fatalf("expected help output for %s to contain %q, got: %s", tt.kind, want, output)
				}
			}
		})
	}
}
