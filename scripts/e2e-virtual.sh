#!/usr/bin/env bash
# Virtual-device E2E runner for Cyborg.
# It builds the CLI, starts virtual targets, runs core actions, and cleans up.

set -u -o pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
BIN="${CYBORG_E2E_BIN:-"$ROOT/bin/cyborg-e2e"}"
ARTIFACT_DIR="${CYBORG_E2E_ARTIFACT_DIR:-"/tmp/cyborg-e2e-virtual"}"
ANDROID_BOOT_TIMEOUT="${CYBORG_ANDROID_BOOT_TIMEOUT:-180}"
IOS_BOOT_TIMEOUT="${CYBORG_IOS_BOOT_TIMEOUT:-120}"
RUN_BROWSER="${CYBORG_E2E_BROWSER:-1}"
RUN_ANDROID="${CYBORG_E2E_ANDROID:-1}"
RUN_IOS="${CYBORG_E2E_IOS:-1}"
KEEP_DEVICES="${CYBORG_E2E_KEEP_DEVICES:-0}"
WDA_URL="${CYBORG_IOS_WDA_URL:-http://127.0.0.1:8100}"

PASSED=()
SKIPPED=()
FAILED=()
IOS_BOOTED_BY_SCRIPT=""
IOS_DEVICE_ID=""
ANDROID_EMULATOR_PID=""

log() {
  printf '\n==> %s\n' "$*"
}

pass() {
  PASSED+=("$1")
  printf 'PASS: %s\n' "$1"
}

skip() {
  SKIPPED+=("$1")
  printf 'SKIP: %s\n' "$1"
}

fail() {
  FAILED+=("$1")
  printf 'FAIL: %s\n' "$1" >&2
}

run() {
  printf '+ %s\n' "$*"
  "$@"
}

json_field() {
  python3 - "$1" "$2" <<'PY'
import json
import sys

path, field = sys.argv[1], sys.argv[2]
with open(path) as f:
    data = json.load(f)
value = data
for part in field.split("."):
    value = value[part]
print(value)
PY
}

first_available_ios_udid() {
  xcrun simctl list devices available -j | python3 -c '
import json
import sys

data = json.load(sys.stdin)
for devices in data.get("devices", {}).values():
    for dev in devices:
        if dev.get("isAvailable") and dev.get("state") in ("Booted", "Shutdown"):
            print(dev["udid"])
            raise SystemExit(0)
raise SystemExit(1)
'
}

first_booted_ios_udid() {
  xcrun simctl list devices booted -j | python3 -c '
import json
import sys

data = json.load(sys.stdin)
for devices in data.get("devices", {}).values():
    for dev in devices:
        if dev.get("isAvailable") and dev.get("state") == "Booted":
            print(dev["udid"])
            raise SystemExit(0)
raise SystemExit(1)
'
}

wait_ios_boot() {
  local udid="$1"
  local deadline=$((SECONDS + IOS_BOOT_TIMEOUT))

  xcrun simctl bootstatus "$udid" -b &
  local pid="$!"
  while kill -0 "$pid" >/dev/null 2>&1; do
    if ((SECONDS >= deadline)); then
      kill "$pid" >/dev/null 2>&1 || true
      wait "$pid" >/dev/null 2>&1 || true
      return 1
    fi
    sleep 1
  done
  wait "$pid"
}

cleanup() {
  if [[ "$KEEP_DEVICES" == "1" ]]; then
    log "Keeping virtual devices because CYBORG_E2E_KEEP_DEVICES=1"
    return
  fi

  if [[ -n "$IOS_DEVICE_ID" ]]; then
    "$BIN" rm "$IOS_DEVICE_ID" >/dev/null 2>&1 || true
  fi
  if [[ -n "$IOS_BOOTED_BY_SCRIPT" ]]; then
    xcrun simctl shutdown "$IOS_BOOTED_BY_SCRIPT" >/dev/null 2>&1 || true
  fi
  if [[ -n "$ANDROID_EMULATOR_PID" ]]; then
    kill "$ANDROID_EMULATOR_PID" >/dev/null 2>&1 || true
  fi
}

reset_daemon() {
  local pids
  pids="$(lsof -ti tcp:58583 2>/dev/null || true)"
  if [[ -n "$pids" ]]; then
    log "Stopping existing Cyborg daemon on port 58583"
    while IFS= read -r pid; do
      [[ -n "$pid" ]] && kill "$pid" >/dev/null 2>&1 || true
    done <<< "$pids"
    sleep 1
  fi
}

build_cli() {
  log "Building Cyborg CLI"
  mkdir -p "$(dirname "$BIN")" "$ARTIFACT_DIR"
  run go build -o "$BIN" ./cmd/cyborg
}

run_browser_e2e() {
  [[ "$RUN_BROWSER" == "1" ]] || { skip "browser disabled"; return; }

  log "Running browser virtual E2E"
  if ! [[ -x "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome" ]] \
    && ! [[ -x "/Applications/Chromium.app/Contents/MacOS/Chromium" ]] \
    && ! command -v google-chrome >/dev/null 2>&1 \
    && ! command -v chromium >/dev/null 2>&1; then
    skip "browser: no Chrome/Chromium runtime found"
    return
  fi

  if CYBORG_E2E=1 go test -v ./internal/cli -run '^TestE2E$' -count=1; then
    pass "browser virtual E2E"
  else
    fail "browser virtual E2E"
  fi
}

android_emulator_bin() {
  if command -v emulator >/dev/null 2>&1; then
    command -v emulator
    return 0
  fi
  for candidate in \
    "${ANDROID_HOME:-}/emulator/emulator" \
    "${ANDROID_SDK_ROOT:-}/emulator/emulator" \
    "$HOME/Library/Android/sdk/emulator/emulator" \
    "/opt/homebrew/share/android-commandlinetools/emulator/emulator" \
    "/usr/local/share/android-commandlinetools/emulator/emulator" \
    "/opt/homebrew/share/android-sdk/emulator/emulator" \
    "/usr/local/share/android-sdk/emulator/emulator"; do
    if [[ -x "$candidate" ]]; then
      printf '%s\n' "$candidate"
      return 0
    fi
  done
  return 1
}

boot_android_emulator() {
  if adb devices | awk 'NR > 1 && $1 ~ /^emulator-/ && $2 == "device" { print $1; exit 0 }' | grep -q .; then
    adb devices | awk 'NR > 1 && $1 ~ /^emulator-/ && $2 == "device" { print $1; exit 0 }'
    return 0
  fi

  local emulator_bin
  if ! emulator_bin="$(android_emulator_bin)"; then
    return 1
  fi
  export ANDROID_AVD_HOME="${ANDROID_AVD_HOME:-"$HOME/.android/avd"}"

  local avd="${CYBORG_ANDROID_AVD:-}"
  if [[ -z "$avd" ]]; then
    avd="$("$emulator_bin" -list-avds 2>/dev/null | head -n 1)"
  fi
  if [[ -z "$avd" ]]; then
    return 1
  fi

  log "Starting Android emulator: $avd"
  "$emulator_bin" -avd "$avd" -no-window -no-audio -no-snapshot-save -gpu swiftshader_indirect >/tmp/cyborg-android-emulator.log 2>&1 &
  ANDROID_EMULATOR_PID="$!"

  local deadline=$((SECONDS + ANDROID_BOOT_TIMEOUT))
  while (( SECONDS < deadline )); do
    local serial
    serial="$(adb devices | awk 'NR > 1 && $1 ~ /^emulator-/ && $2 == "device" { print $1; exit 0 }')"
    if [[ -n "$serial" ]]; then
      local booted
      booted="$(adb -s "$serial" shell getprop sys.boot_completed 2>/dev/null | tr -d '\r')"
      if [[ "$booted" == "1" ]]; then
        echo "$serial"
        return 0
      fi
    fi
    sleep 3
  done
  return 1
}

run_android_e2e() {
  [[ "$RUN_ANDROID" == "1" ]] || { skip "android disabled"; return; }

  log "Running Android emulator E2E"
  if ! command -v adb >/dev/null 2>&1; then
    skip "android: adb not found"
    return
  fi

  local serial
  if ! serial="$(boot_android_emulator)"; then
    skip "android: no booted emulator and no launchable AVD"
    return
  fi

  if CYBORG_E2E=1 CYBORG_E2E_ANDROID=1 CYBORG_ANDROID_SERIAL="$serial" go test -v ./internal/cli -run '^TestE2E_Android$' -count=1; then
    pass "android emulator E2E ($serial)"
  else
    fail "android emulator E2E ($serial)"
  fi
}

run_ios_e2e() {
  [[ "$RUN_IOS" == "1" ]] || { skip "ios disabled"; return; }

  log "Running iOS simulator E2E"
  if ! command -v xcrun >/dev/null 2>&1; then
    skip "ios: xcrun not found"
    return
  fi

  local udid="${CYBORG_IOS_UDID:-}"
  if [[ -z "$udid" ]]; then
    udid="$(first_booted_ios_udid 2>/dev/null || true)"
  fi
  if [[ -z "$udid" ]]; then
    udid="$(first_available_ios_udid 2>/dev/null || true)"
    if [[ -z "$udid" ]]; then
      skip "ios: no available simulator"
      return
    fi
    log "Booting iOS simulator: $udid"
    xcrun simctl boot "$udid" >/dev/null 2>&1 || true
    if ! wait_ios_boot "$udid"; then
      fail "ios: simulator did not boot"
      return
    fi
    IOS_BOOTED_BY_SCRIPT="$udid"
  fi

  local create_json="$ARTIFACT_DIR/ios-device.json"
  if ! "$BIN" up ios --udid="$udid" > "$create_json"; then
    fail "ios: cyborg up ios"
    return
  fi
  IOS_DEVICE_ID="$(json_field "$create_json" id)"

  if ! "$BIN" do screenshot --device="$IOS_DEVICE_ID" --path="$ARTIFACT_DIR/ios-screenshot.png"; then
    fail "ios: screenshot"
    return
  fi
  if ! "$BIN" do launch --device="$IOS_DEVICE_ID" --bundle-id=com.apple.Preferences; then
    fail "ios: launch Settings"
    return
  fi
  if ! "$BIN" do terminate --device="$IOS_DEVICE_ID" --bundle-id=com.apple.Preferences; then
    fail "ios: terminate Settings"
    return
  fi
  pass "ios simulator core actions ($udid)"

  if curl -fsS --max-time 2 "$WDA_URL/status" >/dev/null 2>&1; then
    local wda_json="$ARTIFACT_DIR/ios-wda-device.json"
    "$BIN" rm "$IOS_DEVICE_ID" >/dev/null 2>&1 || true
    IOS_DEVICE_ID=""
    if "$BIN" up ios --udid="$udid" --wda-url="$WDA_URL" > "$wda_json"; then
      IOS_DEVICE_ID="$(json_field "$wda_json" id)"
      if "$BIN" do tree --device="$IOS_DEVICE_ID" && "$BIN" do click --device="$IOS_DEVICE_ID" --target=xy:120,300; then
        pass "ios simulator WDA actions"
      else
        fail "ios simulator WDA actions"
      fi
    else
      fail "ios: cyborg up ios with WDA"
    fi
  else
    skip "ios WDA actions: $WDA_URL is not reachable"
  fi
}

summary() {
  log "Virtual E2E summary"
  printf 'Passed: %d\n' "${#PASSED[@]}"
  printf 'Skipped: %d\n' "${#SKIPPED[@]}"
  printf 'Failed: %d\n' "${#FAILED[@]}"
  for item in "${PASSED[@]}"; do printf '  PASS %s\n' "$item"; done
  for item in "${SKIPPED[@]}"; do printf '  SKIP %s\n' "$item"; done
  for item in "${FAILED[@]}"; do printf '  FAIL %s\n' "$item"; done

  if ((${#FAILED[@]} > 0)); then
    return 1
  fi
  return 0
}

main() {
  cd "$ROOT"
  trap cleanup EXIT
  build_cli
  reset_daemon
  run_browser_e2e
  run_android_e2e
  run_ios_e2e
  "$BIN" ls >/dev/null 2>&1 || true
  summary
}

main "$@"
