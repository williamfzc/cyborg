#!/usr/bin/env bash
# Installer smoke tests for Cyborg.
# It verifies the public installer flow without touching the network or system bin directories.

set -eu -o pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

archive_dir="$TMP_DIR/archive/cyborg-v9.9.9-darwin-arm64"
mkdir -p "$archive_dir"
cat > "$archive_dir/cyborg" <<'BIN'
#!/usr/bin/env bash
printf 'cyborg test build\n'
BIN
chmod +x "$archive_dir/cyborg"
tar -C "$TMP_DIR/archive" -czf "$TMP_DIR/cyborg-v9.9.9-darwin-arm64.tar.gz" "cyborg-v9.9.9-darwin-arm64"

cat > "$TMP_DIR/curl" <<'CURL'
#!/usr/bin/env bash
set -eu

out=""
url=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    -o|--output)
      out="$2"
      shift 2
      ;;
    http*)
      url="$1"
      shift
      ;;
    *)
      shift
      ;;
  esac
done

case "$url" in
  https://api.github.com/repos/williamfzc/cyborg/releases/latest)
    printf '{"tag_name":"v9.9.9"}'
    ;;
  https://github.com/williamfzc/cyborg/releases/download/v9.9.9/cyborg-v9.9.9-darwin-arm64.tar.gz)
    cp "$CYBORG_TEST_ARCHIVE" "$out"
    ;;
  *)
    printf 'unexpected url: %s\n' "$url" >&2
    exit 1
    ;;
esac
CURL
chmod +x "$TMP_DIR/curl"

CYBORG_INSTALL_CURL="$TMP_DIR/curl" \
CYBORG_INSTALL_DIR="$TMP_DIR/bin" \
CYBORG_INSTALL_NO_GH="1" \
CYBORG_INSTALL_OS="darwin" \
CYBORG_INSTALL_ARCH="arm64" \
CYBORG_TEST_ARCHIVE="$TMP_DIR/cyborg-v9.9.9-darwin-arm64.tar.gz" \
  "$ROOT/install.sh"

"$TMP_DIR/bin/cyborg" | grep -q 'cyborg test build'

printf 'install smoke test passed\n'
