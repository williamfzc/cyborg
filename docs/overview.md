# Overview

Cyborg is a local device control plane. It starts a managed daemon, creates controllable device targets, and sends stateless actions to those targets through one CLI surface.

## Command Shape

The public command is `cyborg`. Seven top-level verbs, no more:

- `cyborg up <kind>` creates a device target.
- `cyborg ls` lists all devices.
- `cyborg show <id>` shows device details.
- `cyborg rm <id>` removes a device.
- `cyborg do <action>` sends an action to a device.
- `cyborg help <kind>` shows actions available for a device kind (queries daemon dynamically).
- `cyborg version` prints the version.

The CLI does not keep a current device. If exactly one device exists, `--device` may be omitted. If multiple devices exist, callers must pass `--device=<id>`.

## Targeting Elements

Actions that operate on UI elements accept a `--target` flag with a strategy prefix:

| Prefix | Meaning | Browser | Android | iOS |
|--------|---------|:---:|:---:|:---:|
| `css:` | CSS selector (browser default) | ✓ | — | — |
| `text:` | Visible text match (android default) | — | ✓ | ✓ |
| `id:` | Platform native ID | ✓ | ✓ | ✓ |
| `acc:` | Accessibility label | ✓ | ✓ | ✓ |
| `xy:` | Screen coordinates | — | ✓ | ✓ |

## Driver Self-Description

Each driver implements `Actions() []ActionSpec`, letting the daemon serve action metadata dynamically via `GET /drivers/<kind>/actions`. This means `cyborg help browser` always reflects the real capabilities — no static text to drift.

## Current Drivers

- `browser`: controls Chromium-compatible browsers through a daemon-owned CDP session.
- `android`: creates or controls adb-managed Android emulator targets through adb and UIAutomator.
- `ios`: boots or controls iOS Simulators through `xcrun simctl`; UI actions use WebDriverAgent at `http://127.0.0.1:8100` by default, with `--wda-url` available for overrides.

New drivers (Docker, remote VM) plug in by implementing the `Driver` interface and registering in the daemon. Zero CLI code changes required.

## Source Links

- [[../AGENTS|Repository rules]]
- [CLI implementation](../internal/cli/cli.go)
- [Driver interface](../internal/core/driver/driver.go)
- [Daemon implementation](../internal/daemon/server.go)
