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

# Name the rc file for the shell the user actually logs into, so every
# instruction below is a runnable command rather than one to translate.
shname=$(basename "${SHELL:-/bin/sh}")
case "$shname" in
  zsh)  rc="$HOME/.zshrc" ;;
  bash) rc="$HOME/.bashrc" ;;
  fish) rc="$HOME/.config/fish/config.fish" ;;
  *)    shname=bash; rc="$HOME/.profile" ;;
esac

# fish has no `eval "$(...)"` and dropped `.` as a source alias.
if [ "$shname" = fish ]; then
  path_cmd="fish_add_path $dest"
  init_cmd="mkdir -p $(dirname "$rc") && nudge init fish >> $rc"
  reload_cmd="source $rc"
else
  path_cmd="echo 'export PATH=\"$dest:\$PATH\"' >> $rc"
  init_cmd="echo 'eval \"\$(nudge init $shname)\"' >> $rc"
  reload_cmd=". $rc"
fi

# Mirrors config.Path(): Go's os.UserConfigDir() is $HOME/Library/Application
# Support on darwin, and $XDG_CONFIG_HOME (else $HOME/.config) on Linux.
if [ "$os" = darwin ]; then
  cfg="$HOME/Library/Application Support/nudge/config.toml"
else
  cfg="${XDG_CONFIG_HOME:-$HOME/.config}/nudge/config.toml"
fi

if [ "$on_path" = no ]; then
  echo
  echo "NOTE: $dest is not on your PATH, so \`nudge\` will not run yet."
  echo "Add it, then reopen your shell (or run the same line in this one):"
  echo
  echo "  $path_cmd"
  [ "$shname" = fish ] || echo "  $reload_cmd"
  echo
  echo "Then verify with:  command -v nudge"
fi

cat <<EOF

next steps:
  1. pick a model — either one:
       a. local (default, private, free per query):
            install Ollama:  https://ollama.com/download  (or your package manager)
            pull the model:  ollama pull qwen2.5-coder:1.5b
       b. cloud (needs an API key; queries leave your machine):
            put  provider = "anthropic"  (or openai / azure / deepseek) in
            $cfg
            and set the matching key, e.g. export ANTHROPIC_API_KEY=...
            details: https://github.com/eduardsjermaks/nudge#choosing-a-brain
  2. enable the shell hook (bare \`nudge\` / \`fix\`):
       $init_cmd
       $reload_cmd
  3. verify:
       nudge doctor
EOF

if [ "$on_path" = no ]; then
  echo
  echo "(do the PATH step above first — steps 2 and 3 run nudge)"
fi
