#!/usr/bin/env sh
# Cyborg release installer.
# It downloads the matching GitHub Release archive and installs the cyborg binary locally.

set -eu

repo="${CYBORG_INSTALL_REPO:-williamfzc/cyborg}"
tag="${CYBORG_INSTALL_TAG:-latest}"
install_dir="${CYBORG_INSTALL_DIR:-"$HOME/.local/bin"}"
curl_bin="${CYBORG_INSTALL_CURL:-curl}"
github_token="${GH_TOKEN:-${GITHUB_TOKEN:-}}"

detect_os() {
  if [ "${CYBORG_INSTALL_OS:-}" != "" ]; then
    printf '%s\n' "$CYBORG_INSTALL_OS"
    return
  fi

  case "$(uname -s)" in
    Darwin) printf 'darwin\n' ;;
    Linux) printf 'linux\n' ;;
    *)
      printf 'Unsupported operating system: %s\n' "$(uname -s)" >&2
      exit 1
      ;;
  esac
}

detect_arch() {
  if [ "${CYBORG_INSTALL_ARCH:-}" != "" ]; then
    printf '%s\n' "$CYBORG_INSTALL_ARCH"
    return
  fi

  case "$(uname -m)" in
    x86_64|amd64) printf 'amd64\n' ;;
    arm64|aarch64) printf 'arm64\n' ;;
    *)
      printf 'Unsupported CPU architecture: %s\n' "$(uname -m)" >&2
      exit 1
      ;;
  esac
}

require_command() {
  if ! command -v "$1" >/dev/null 2>&1; then
    printf 'Required command not found: %s\n' "$1" >&2
    exit 1
  fi
}

have_gh() {
  if [ "${CYBORG_INSTALL_NO_GH:-}" = "1" ]; then
    return 1
  fi

  command -v gh >/dev/null 2>&1 && gh -R "$repo" release list --limit 1 >/dev/null 2>&1
}

latest_tag() {
  if have_gh; then
    gh -R "$repo" release view --json tagName --jq .tagName
    return
  fi

  require_command "$curl_bin"
  if [ "$github_token" != "" ]; then
    "$curl_bin" -fsSL -H "Authorization: Bearer $github_token" "https://api.github.com/repos/$repo/releases/latest"
  else
    "$curl_bin" -fsSL "https://api.github.com/repos/$repo/releases/latest"
  fi |
    sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' |
    head -n 1
}

download_archive() {
  if have_gh; then
    gh -R "$repo" release download "$tag" --pattern "$archive" --dir "$tmp_dir" --clobber
    return
  fi

  require_command "$curl_bin"
  if [ "$github_token" != "" ]; then
    "$curl_bin" -fL --progress-bar -H "Authorization: Bearer $github_token" "$url" -o "$tmp_dir/$archive"
  else
    "$curl_bin" -fL --progress-bar "$url" -o "$tmp_dir/$archive"
  fi
}

require_command sed
require_command tar
require_command mktemp

os="$(detect_os)"
arch="$(detect_arch)"

if [ "$tag" = "latest" ]; then
  tag="$(latest_tag)"
fi

if [ "$tag" = "" ]; then
  printf 'Could not resolve latest Cyborg release tag.\n' >&2
  exit 1
fi

archive="cyborg-$tag-$os-$arch.tar.gz"
url="https://github.com/$repo/releases/download/$tag/$archive"
tmp_dir="$(mktemp -d)"
trap 'rm -rf "$tmp_dir"' EXIT HUP INT TERM

printf 'Installing Cyborg %s for %s-%s...\n' "$tag" "$os" "$arch"

download_archive
mkdir -p "$tmp_dir/extract" "$install_dir"
tar -xzf "$tmp_dir/$archive" -C "$tmp_dir/extract"

binary_path="$(find "$tmp_dir/extract" -type f -name cyborg | head -n 1)"
if [ "$binary_path" = "" ]; then
  printf 'Downloaded archive did not contain a cyborg binary.\n' >&2
  exit 1
fi

cp "$binary_path" "$install_dir/cyborg"
chmod +x "$install_dir/cyborg"

printf 'Cyborg installed at %s\n' "$install_dir/cyborg"

case ":$PATH:" in
  *":$install_dir:"*) ;;
  *)
    printf 'Add %s to PATH if cyborg is not found in new shells.\n' "$install_dir"
    ;;
esac

"$install_dir/cyborg" version >/dev/null 2>&1 || "$install_dir/cyborg" --version >/dev/null 2>&1 || true
