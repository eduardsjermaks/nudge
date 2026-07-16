#!/bin/sh
# nudge installer (Linux/macOS) — usage:  curl -fsSL https://raw.githubusercontent.com/eduardsjermaks/nudge/main/install.sh | sh
# Downloads the right binary into ~/.local/bin (or /usr/local/bin if writable).
set -eu

# Override for testing against a different tag or release host.
BASE_URL="${NUDGE_RELEASE_URL:-https://github.com/eduardsjermaks/nudge/releases/latest/download}"

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

on_path=yes
case ":$PATH:" in
  *":$dest:"*) ;;
  *) on_path=no ;;
esac

# Name the rc file for the shell the user actually logs into, so the PATH
# instruction below is a runnable command rather than one to translate.
shname=$(basename "${SHELL:-/bin/sh}")
case "$shname" in
  zsh)  rc="$HOME/.zshrc" ;;
  bash) rc="$HOME/.bashrc" ;;
  fish) rc="$HOME/.config/fish/config.fish" ;;
  *)    shname=bash; rc="$HOME/.profile" ;;
esac

if [ "$on_path" = no ]; then
  echo
  echo "NOTE: $dest is not on your PATH, so \`nudge\` will not run yet."
  echo "Add it, then reopen your shell (or run the same line in this one):"
  echo
  if [ "$shname" = fish ]; then
    echo "  fish_add_path $dest"
  else
    echo "  echo 'export PATH=\"$dest:\$PATH\"' >> $rc"
    echo "  . $rc"
  fi
  echo
  echo "Then verify with:  command -v nudge"
fi

cat <<'EOF'

next step — run the wizard:

  nudge setup

It configures a cloud provider (or installs Ollama and pulls the model,
if you prefer local), and adds the shell hook — asking before every change.
Safe to re-run any time. Manual steps, if you prefer doing it by hand:
https://github.com/eduardsjermaks/nudge#install-5-minutes
EOF

if [ "$on_path" = no ]; then
  echo
  echo "(do the PATH step above first — nudge setup needs nudge on your PATH)"
fi
