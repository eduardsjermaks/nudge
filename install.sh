#!/bin/sh
# nudge installer (Linux/macOS) — usage:  curl -fsSL https://<release-url>/install.sh | sh
# Downloads the right binary into ~/.local/bin (or /usr/local/bin if writable).
set -eu

# Update this when releases are published.
BASE_URL="${NUDGE_RELEASE_URL:-https://example.com/nudge/releases/latest}"

os=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$os" in
  linux|darwin) ;;
  *) echo "unsupported OS: $os" >&2; exit 1 ;;
esac
arch=$(uname -m)
case "$arch" in
  x86_64|amd64) arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  *) echo "unsupported arch: $arch" >&2; exit 1 ;;
esac

dest="$HOME/.local/bin"
[ -w /usr/local/bin ] && dest=/usr/local/bin
mkdir -p "$dest"

echo "downloading nudge (${os}_${arch})..."
curl -fsSL "$BASE_URL/nudge_${os}_${arch}" -o "$dest/nudge"
chmod +x "$dest/nudge"

echo "nudge installed: $dest/nudge"
case ":$PATH:" in
  *":$dest:"*) ;;
  *) echo "NOTE: $dest is not on your PATH — add it to your shell profile." ;;
esac

cat <<'EOF'

next steps:
  1. install Ollama:  https://ollama.com/download  (or your package manager)
  2. pull the model:  ollama pull qwen2.5-coder:1.5b
  3. add to your rc:  eval "$(nudge init bash)"     # or zsh / fish
  4. verify:          nudge doctor
EOF
