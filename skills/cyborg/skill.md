---
name: cyborg
description: |
  Provision on-demand VMs and emulated devices (browsers, Android emulators) for AI agents.
  Cyborg creates ephemeral, isolated environments — spin up when needed, destroy when done.
  Use when the user needs: a browser/emulator for an agent, isolated test environment, UI automation on emulator.
  Trigger phrases: "spin up browser", "create device", "browser automation", "run in VM", "test on emulator",
  "on-demand device", "ephemeral environment", "cyborg", "拉起浏览器", "创建设备", "浏览器自动化",
  "在模拟器上测试", "设备自动化", "手机操作", "Android 自动化", "给 agent 一个设备".
  NOT for: puppeteer/playwright (different tools), unit tests, CSS fixes, real device debugging.
---

# Cyborg Skill — Device Provisioning for Agents

You can spin up on-demand virtual machines and emulated devices (browsers, Android emulators) for AI agents.
All commands are stateless. The daemon manages device lifecycle automatically — create when needed, destroy when done.

## Core Workflow

```bash
# 1. Check existing devices first (avoid unnecessary creation)
cyborg ls

# 2. If no suitable device exists, create one
cyborg up browser --headless
cyborg up android --serial=emulator-5554

# 3. Execute actions (--device omitted when only one device exists)
cyborg do <action> [--device=<id>] [flags]

# 4. Remove when done
cyborg rm <device-id>
```

## Commands

| Command | Purpose |
|---------|---------|
| `cyborg ls` | List all devices — this is your pool status |
| `cyborg up <kind> [opts]` | Create a device |
| `cyborg show <id>` | Device details + capabilities |
| `cyborg rm <id>` | Destroy a device |
| `cyborg do <action> [flags]` | Execute an action |
| `cyborg help <kind>` | List actions supported by a kind |

## Targeting Elements (--target)

UI actions use a single `--target` flag with a strategy prefix:

| Prefix | When to use | Example |
|--------|-------------|---------|
| `css:` | Browser (default) | `--target="css:button.submit"` |
| `text:` | Android/iOS (default) | `--target="text:Login"` |
| `id:` | When you know the native ID | `--target="id:com.app:id/btn_login"` |
| `acc:` | Accessibility label | `--target="acc:submit_button"` |
| `xy:` | Coordinates — last resort | `--target="xy:540,1200"` |

If unsure which strategy to use, prefer `css:` for browsers and `text:` for Android.

## Browser Actions

```bash
cyborg do open --url=https://example.com
cyborg do click --target="css:#login-btn"
cyborg do type --target="css:input[name=email]" --text="user@test.com"
cyborg do press --key=Enter
cyborg do screenshot
cyborg do eval --code="document.title"
```

## Android Actions

```bash
cyborg do click --target="text:Settings"
cyborg do click --target="xy:540,1200"
cyborg do type --text="hello world"
cyborg do press --key=home
cyborg do swipe --from=540,1500 --to=540,500
cyborg do screenshot
cyborg do tree                              # dump UI hierarchy XML
cyborg do shell --cmd="am start -n com.app/.MainActivity"   # launch app
cyborg do shell --cmd="pm list packages"
cyborg do install --apk=/path/to/app.apk
```

## Decision Logic

Before every action:

1. Run `cyborg ls` to see what's available.
2. If a `state=running` device of the right `kind` exists → use it directly.
3. If no device exists or the existing one is `stopped` → `cyborg rm` the dead one, then `cyborg up`.
4. If multiple devices exist, always pass `--device=<id>`.

## Important Rules

- Never create a device without checking `ls` first.
- One device is usually enough. Don't hoard.
- `screenshot` after important actions to verify the result visually.
- If an action returns `ok=false`, read the error message — don't retry blindly.
- `tree` (android) gives you the full UI structure — use it to find valid targets before clicking.
