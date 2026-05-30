# Cyborg

Cyborg gives agents one control plane for temporary virtual devices. During development and daily work, it lets you bring up a browser, Android emulator, or iOS simulator when you need to preview real behavior, verify a flow, capture evidence, or hand a live target to an agent.

The detailed architecture and command contract live in the docs vault. Start with [docs/index.md](docs/index.md), then read [docs/overview.md](docs/overview.md) for the current command shape, target strategies, driver model, and automation notes.

## Why It Exists

Real work often needs a device for a few minutes, not a permanent lab. Cyborg keeps that loop small across environments:

```bash
cyborg up browser
cyborg do open --url=https://example.com
cyborg do screenshot
cyborg rm <device-id>
```

For the exact command rules and device selection behavior, see [Command Shape](docs/overview.md#command-shape).

## Control Plane

Cyborg's competitive point is not any single backend. It is the common surface across backends: bring up a target on demand, discover what it can do, send actions, collect results, and tear it down when the preview or verification is done.

The current driver list and behavior notes live in [Current Drivers](docs/overview.md#current-drivers).

## Install

For macOS and Linux, install the latest release with:

```bash
curl -fsSL https://raw.githubusercontent.com/williamfzc/cyborg/main/install.sh | sh
```

The installer places `cyborg` in `~/.local/bin`. Add that directory to your `PATH` if your shell does not already load it.

### Agent Install

When installing Cyborg for a user, agents should avoid prompts and system-wide writes:

```bash
mkdir -p "$HOME/.local/bin"
curl -fsSL https://raw.githubusercontent.com/williamfzc/cyborg/main/install.sh | CYBORG_INSTALL_DIR="$HOME/.local/bin" sh
export PATH="$HOME/.local/bin:$PATH"
cyborg version
```

If the user asks for a specific version, set `CYBORG_INSTALL_TAG`:

```bash
curl -fsSL https://raw.githubusercontent.com/williamfzc/cyborg/main/install.sh | CYBORG_INSTALL_TAG=v0.1.1 sh
```

## Install From Source

```bash
go build -trimpath -o bin/cyborg ./cmd/cyborg
```

Then either run `bin/cyborg` directly or put it on your `PATH`.

## Basic Workflow

```bash
cyborg ls
cyborg up browser --headless
cyborg help browser
cyborg do open --url=https://example.com
cyborg do screenshot
cyborg rm <device-id>
```

Use `cyborg help <kind>` to ask the daemon for the real action list of a device kind. This keeps help output tied to the registered driver instead of duplicating capability tables in the README.

## Documentation Map

- [Docs index](docs/index.md): the entry point for the Obsidian-style project docs.
- [Overview](docs/overview.md): project scope, command shape, target strategy prefixes, driver self-description, and CI/release notes.
- [Cyborg skill](skills/cyborg/skill.md): agent-facing operating guide for deciding when and how to create devices.
- [Repository rules](AGENTS.md): working rules for keeping the code and docs coherent.

## Development

```bash
go test ./...
go build -trimpath -o /tmp/cyborg ./cmd/cyborg
```

Virtual-device checks are available through:

```bash
scripts/e2e-virtual.sh
```

Those checks use local browser, Android, and iOS simulator tooling when present, and skip unavailable platforms instead of failing the whole run.

## Releases

Release behavior is defined by the repository workflows:

- [CI workflow](.github/workflows/ci.yml)
- [Release workflow](.github/workflows/release.yml)

## License

Cyborg is licensed under the [Apache License 2.0](LICENSE).
