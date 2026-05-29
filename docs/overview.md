# Overview

Cyborg is a local device control plane. It starts a managed daemon, creates controllable device targets, and sends stateless actions to those targets through one CLI surface.

## Command Shape

The public command is `cyborg`.

- `cyborg up <browser|android>` creates a device target.
- `cyborg do <action> --device=<id>` sends a universal action.
- `cyborg browser <action> --device=<id>` sends a browser-specific action.
- `cyborg android <action> --device=<id>` sends an Android-specific action.

The CLI does not keep a current device. If exactly one device exists, `--device` may be omitted. If multiple devices exist, callers must pass `--device=<id>`.

## Current Drivers

- `browser`: controls Chromium-compatible browsers through a daemon-owned session.
- `android`: controls adb-attached real devices or emulators through the same action protocol.

## Source Links

- [[../AGENTS|Repository rules]]
- [CLI implementation](../internal/cli/cli.go)
- [Daemon implementation](../internal/daemon/server.go)
