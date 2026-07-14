# nudge

Type the command you *meant*. A local LLM figures it out; you confirm; it runs.
**Nothing ever leaves your machine.**

```
PS> nudge dotnet create migrations
`dotnet create migrations` isn't a valid command. Did you mean:
  → dotnet ef migrations add <name>    (create a new EF Core migration)
  value for <name>: AddOrders
  will run: dotnet ef migrations add AddOrders
Run it? [Enter = yes / n = no / e = edit]
```

It fixes two kinds of mistakes:

- **Fix mode** — you ran something that failed or doesn't exist:
  `git pshu` → `git push` · `docker remove image foo` → `docker rmi foo` ·
  `dotnet create migrations` → `dotnet ef migrations add <name>`
- **Intent mode** — you say what you want in plain words:
  `nudge undo last commit` → `git reset --soft HEAD~1` ·
  `nudge new migration AddOrders` → `dotnet ef migrations add AddOrders`
  (it saw your `.csproj`)

Pure typos (`git pshu`, `gti status`) are fixed **instantly without the model**
by a matcher built at runtime from your own `PATH` and your tools' own help
output. There are no pattern files or rules to maintain — anywhere.

## Install (5 minutes)

**1. Get the binary** — download the release for your platform and put it on
your `PATH`, or:

```powershell
# Windows (PowerShell)
irm https://raw.githubusercontent.com/eduardsjermaks/nudge/main/install.ps1 | iex
```
```bash
# Linux / macOS
curl -fsSL https://raw.githubusercontent.com/eduardsjermaks/nudge/main/install.sh | sh
```

The install scripts download binaries from the latest GitHub release. Building
from source: `go build ./cmd/nudge` — no CGO, no exotic deps.

**2. Install a local model server** — [Ollama](https://ollama.com/download)
is the default:

```
ollama pull qwen2.5-coder:1.5b
```

**3. Add one line to your shell profile** (optional but recommended — enables
bare `nudge` / `fix` and automatic catch of misspelled binaries):

| Shell | Add to | Line |
|---|---|---|
| PowerShell | `$PROFILE` | `Invoke-Expression (& nudge init pwsh \| Out-String)` |
| bash | `~/.bashrc` | `eval "$(nudge init bash)"` |
| zsh | `~/.zshrc` | `eval "$(nudge init zsh)"` |
| fish | `~/.config/fish/config.fish` | `nudge init fish \| source` |

Check the setup: `nudge doctor`.

### Honest numbers

| | |
|---|---|
| nudge binary | ~3 MB, <50 ms startup, <30 MB RSS |
| model on disk | ~1.0 GB (`qwen2.5-coder:1.5b`) |
| model in RAM | ~1.2 GB **inside the Ollama server** while loaded (`keep_alive`, default 10 m) |
| typo fixes (Tier 1) | < 10 ms, no model involved |
| model answers, modern laptop CPU | ~1–2 s warm |
| model answers, older 4-core ultrabook (2018, no GPU) | 5–8 s warm — usable, not snappy |
| first call after idle | + model load, roughly 2–5 s |

The memory belongs to the model server you installed, not to nudge — but you
pay it either way, so it's listed.

## Using it

**Level 1 — explicit (works everywhere, even cmd.exe):**

```
nudge git pshu                # fix a typo
nudge undo last commit        # describe what you want
nudge docker remove image alpine
```

**Level 2 — bare `nudge` / `fix` (needs the init line):** after a command
fails, just type `nudge` (or `fix`). The wrapper function reads your last
command and its exit code from shell history and proposes the correction.
This is the only way to catch mistakes like `dotnet create migrations`, where
the binary exists but the invocation is wrong. Suggestions that change shell
state (`cd`, activating a venv) work at this level, because the shell itself
runs the confirmed command.

**Level 3 — automatic (needs the init line):** mistype a *binary name*
(`gti status`) and the shell's command-not-found hook calls nudge for you.
Note honestly: these hooks only fire when the binary doesn't exist; a wrong
subcommand of a real tool returns exit ≠ 0 and no hook fires — that's what
`fix` is for.

At the prompt: **Enter** runs, **n** aborts, **e** edits the line first.
Destructive suggestions (`rm -rf`, `git reset --hard`, force-push, `prune`,
…) and low-confidence guesses never accept bare Enter — they demand a typed
`y` after a warning. Non-TTY output prints the suggestion and exits 3 without
running anything. The executed command's exit code is propagated.

`--explain` shows which tier answered and how long it took.

## Configuration (infrastructure only — there is no matching config)

`%APPDATA%\nudge\config.toml` on Windows, `~/.config/nudge/config.toml`
elsewhere. Env vars in parentheses override the file:

```toml
endpoint   = "http://localhost:11434"   # (NUDGE_ENDPOINT) must be loopback
model      = "qwen2.5-coder:1.5b"       # (NUDGE_MODEL)
backend    = "ollama"                    # (NUDGE_BACKEND) "ollama" | "openai"
keep_alive = "10m"                       # (NUDGE_KEEP_ALIVE) how long the model stays warm
timeout    = 30                          # (NUDGE_TIMEOUT) seconds
confidence = 0.6                         # (NUDGE_CONFIDENCE) below this = "best guess" label
# allow_non_local = true                 # (NUDGE_ALLOW_NON_LOCAL) see privacy
```

**Better quality:** `model = "qwen2.5-coder:3b"` (~2 GB) is noticeably
smarter and the recommended upgrade if your machine keeps up.

**Other backends:** anything OpenAI-compatible on localhost works — LM Studio,
llama.cpp server, vLLM:

```toml
backend  = "openai"
endpoint = "http://localhost:1234"   # LM Studio default
model    = "loaded-model-name"
```

**Degraded mode:** if the model server is down, typo fixes (Tier 1) keep
working and model queries fail with a one-line hint to run `nudge doctor`,
which checks endpoint, model, JSON mode, and measures a warm-call latency.

## Privacy

- **Nothing leaves your machine.** nudge talks only to a loopback address and
  hard-fails on any non-local endpoint unless you explicitly set
  `allow_non_local = true` (for e.g. a model box on your LAN — your call, your
  data). There is no telemetry, no analytics, no update check, no cloud
  fallback. The binary makes no network connection other than your model
  server.
- **What is sent to the local model, exactly:** your typed command (or intent
  text), its exit code, your OS and shell name, and the *names* of project
  marker files in the current directory (`Api.csproj`, `package.json`, …).
  **Never file contents**, never environment variables, never directory
  listings beyond those marker names.
- Tier 1 reads executable names from `PATH` and runs `tool --help` for a known
  set of CLIs to learn their subcommands; results are cached locally in your
  user cache directory.

## Uninstall

1. Remove the init line from your shell profile.
2. Delete the binary.
3. Delete config and cache: `%APPDATA%\nudge` + `%LOCALAPPDATA%\nudge`
   (Windows), `~/.config/nudge` + `~/.cache/nudge` (Linux),
   `~/Library/Caches/nudge` (macOS).
4. If you installed Ollama only for this: `ollama rm qwen2.5-coder:1.5b` and
   uninstall Ollama.

## Development

```
go test ./...                      # unit tests, no model needed
NUDGE_EVAL=1 go test ./eval -v     # model eval (needs a local model)
make build-all                     # cross-compile windows/linux/darwin × amd64/arm64
```

On Windows, `make` is optional. The repo includes a PowerShell equivalent:

```powershell
.\build-release.ps1 -Version v0.1.0
```

## Releases

Pushing a version tag automatically tests, cross-compiles all supported targets,
creates a GitHub Release, and uploads the installer assets. The generated
`dist/` directory deliberately stays out of Git.

1. Pick a version, for example `v0.1.0`.
2. Commit and push the changes you want to release.
3. Create and push the tag: `git tag v0.1.0` and `git push origin v0.1.0`
4. Watch the **Release** workflow in the repository's Actions tab. When it
  succeeds, the release and its six binary assets are published automatically.

The workflow runs `go test ./...` before publishing. If the test or build step
fails, no release is created. After a successful release, the install scripts
resolve `releases/latest/download/...` automatically.

For a local build without publishing, install Go from https://go.dev/dl/ and
run `make build-all VERSION=v0.1.0` on Unix-like systems or
`.\build-release.ps1 -Version v0.1.0` in PowerShell.

See `DESIGN.md` for the two-tier architecture, the no-configuration
principle, safety validation, and shell-integration tradeoffs. Scoop and
Homebrew packaging are future work.
