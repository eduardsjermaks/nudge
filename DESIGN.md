# nudge — design notes

`nudge` proposes the command you meant. You confirm; it runs. All understanding
comes from a local LLM plus names harvested from your own machine — there is no
pattern file, synonym list, or rule config anywhere in this project, and there
never will be.

## The two-tier design

**Tier 1 — derived typo fixer.** Pure keyboard slips (`git pshu`, `gti statsu`)
don't deserve a model call: waking a 1.5B model costs seconds of CPU and ~1 GB
of RAM residency to fix a transposed letter that edit distance resolves in
microseconds. Tier 1 is instant (<10 ms warm), free, and — crucially — needs
zero configuration because every name it matches against is *derived from the
machine at runtime*:

- **Executable names** are harvested from `PATH` (with `PATHEXT` handling on
  Windows), cached keyed on a hash of the `PATH` value with a 24 h TTL.
- **Subcommand names** are harvested from the tools' own help output
  (`git help -a`, `dotnet --help`, `docker --help`, …) for a known set of
  multi-command CLIs. The set names which tools to *ask*; the answers always
  come from the tool itself, so they are exactly right for the installed
  version. Parsed with a generic heuristic (indented lines whose first token
  looks like a command word; fully command-like columned lines contribute all
  tokens). Cached per tool identity (path + size + mtime of the binary), so a
  tool upgrade re-harvests automatically. If parsing yields fewer than 3
  candidates it is treated as failure and Tier 1 silently skips that tool.

**Matching:** restricted Damerau-Levenshtein (optimal string alignment) with a
deliberately strict confidence bar, because a wrong instant fix is worse than a
2-second model call:

- distance limit 1 for words up to 6 chars, 2 above; words under 3 chars are
  never corrected;
- scores are half-stepped: a same-letter-multiset scramble at edit distance 2
  (`pshu` → `push`) scores 1.5 — inside the bar but strictly worse than a
  clean single edit, so `gti` resolves to `git` and not its anagram `tig`;
- the best candidate must be *unique*; any tie kills the match;
- 3-letter first words are only corrected on pure letter scrambles
  (`gti` → `git` yes, `new` → `net` no — that one is an intent phrase);
- if the first word was corrected and the second word isn't a valid or
  correctable subcommand of the corrected tool, Tier 1 refuses rather than
  ship half a fix.

Everything below the bar falls through to Tier 2. Intent phrases ("undo last
commit") fall through by construction: their words aren't near-misses of
anything real.

**Tier 2 — the configured model provider.** The prompt contains exactly: OS +
shell name, project marker file *names* from the cwd (`*.csproj`,
`package.json`, `go.mod`, … — never file contents), the user's input, and in
fix mode the exit code. The model must return strict JSON (`command`,
`explanation`, `confidence`, `placeholders`, `destructive`, `shell_state`).
Post-hoc *stderr* capture of the failed command is not portably possible
without wrapping every process, so the model only gets the exit code — richer
failure context is future work.

## Providers: two wire protocols, one active provider

Tier 2 defaults to a local Ollama model but can be pointed at a cloud
provider (OpenAI, Azure OpenAI, Anthropic, DeepSeek) or any OpenAI-compatible
server (`custom`). Two decisions shape the implementation:

**Two wire protocols, not five clients.** OpenAI, Azure, DeepSeek, and
`custom` all speak OpenAI-style chat completions; only auth style, base URL,
and default model differ — those are data (`internal/provider` presets), not
code. Anthropic's Messages API is the one genuinely different shape
(`system` top-level, `x-api-key` + `anthropic-version` headers, content
blocks) and gets its own client. Ollama keeps its native API for
`keep_alive` and JSON mode. Wrinkle discovered by the live tests: openai.com's
current models renamed `max_tokens` to `max_completion_tokens` and reject
explicit `temperature`; the compatible servers still speak the classic
dialect, so the OpenAI-protocol client switches dialect on the provider name.

**No silent escalation to cloud — a hard rule.** Exactly one provider is
active, selected in config. Nothing is ever sent to a cloud provider unless
the user explicitly selected one: if the active provider is local and
unreachable, nudge degrades to Tier 1 with a doctor hint, and an
`OPENAI_API_KEY` sitting in the environment is never touched. This falls out
of the structure (only the active provider's client is ever constructed) and
is pinned by a test. No multi-provider fallback chains in this version.

Provider JSON modes (OpenAI/DeepSeek `response_format`) are used where
offered, but only as an optimization: the local validator in
`suggest.Parse` plus one retry remains the only source of truth. Anthropic
has no JSON mode here — the system prompt demands bare JSON, and assistant
prefill (the classic trick) is not used because current Anthropic models
reject it.

**Secret masking (cloud only).** Before a query leaves the machine,
`internal/mask` replaces likely secrets — known key prefixes, bearer tokens,
password-flag values, high-entropy tokens — with stable `«SECRET_n»`
placeholders, restored verbatim in the returned command. Detection errs
toward masking: a masked commit SHA is restored verbatim and costs only
context; a missed secret leaves the machine. Local providers skip the pass.

**Credentials** resolve standard-env-var → `api_key_env` indirection →
plaintext `api_key` (warned, file chmod 0600 on POSIX). Keys never appear in
logs, errors, or doctor output. OS keychain integration is future work.

`NUDGE_HTTP_LOG=<file>` appends every outgoing request URL + body (never
headers) — the audit hook for "what actually left the machine".

## Model choice and prompt design

Default: `qwen2.5-coder:1.5b` (~1 GB on disk, ~1.2 GB resident in the model
server). It is the smallest model we found that reliably (a) follows the JSON
schema at temperature 0, (b) knows the long tail of CLI surface (EF Core,
kubectl flags), and (c) honestly returns confidence 0 for nonsense.
`qwen2.5-coder:3b` is the documented quality upgrade. Temperature 0,
`num_predict` capped at 200, Ollama JSON mode (`format: "json"`) or OpenAI
`response_format: json_object` — plus hard validation on our side regardless,
because JSON mode guarantees syntax, not schema.

The system prompt lives in an embedded `prompt.txt` — prompt engineering, not
configuration — with few-shot examples covering the acceptance cases. It is
kept deliberately short (~600 tokens): the model server caches the KV prefix
for the shared system prompt across calls, so after the first call only the
user suffix is evaluated and warm latency is generation-bound.

**Latency honesty:** generation is ~40 output tokens. On a modern laptop CPU
(30–60 tok/s for a 1.5B Q4 model) that is 1–2 s warm, within budget. On the
2018 4-core ultrabook this was developed on (~8 tok/s, no GPU) warm calls take
5–8 s. The budget assumption is documented in the README rather than hidden.
`keep_alive` (default 10 m, configurable) keeps the model and its prefix cache
resident between calls.

## The no-configuration principle

Anything that decides *what a command means* must come from either the machine
(Tier 1 harvesting) or the model (Tier 2). Users maintain nothing:

- no rule/glob/synonym files, embedded or in a config dir;
- the config file that does exist (`config.toml`) holds only infrastructure:
  provider, endpoint, model, credentials, keep-alive, timeouts, confidence
  threshold;
- `eval/cases.json` is grading data for the test suite; the binary never
  reads it;
- the destructive-pattern detector (below) is a fixed safety property of the
  tool, like a compiler's warning list — it decides how *carefully* to run a
  suggestion, never what to suggest.

## Safety validation

Model output is untrusted input:

1. **Parse hard.** Strip markdown fences/backticks/`$ ` prompts; extract the
   outermost JSON object; schema-validate; cap to a single command line and
   500 chars; confidence must be in [0,1]. Invalid → one retry → give up
   (exit 1). Nothing that failed validation is ever shown as runnable, let
   alone run.
2. **Placeholders** (`{name}`) are reconciled with the command text (listed
   ones missing from the command are dropped; unlisted ones found in the
   command are added) and always prompted for before the confirm step, so the
   user sees exactly the final line that will run.
3. **Destructive detection** is deterministic and local: `rm -rf`, `--hard`,
   force-push, `clean`, `prune`, `drop`/`truncate`, `dd of=`, `mkfs`/`format`,
   `del /s`, `Remove-Item -Recurse/-Force`, `>` over files, fork bombs, etc.
   The model's `destructive` flag can only escalate, never clear, a local
   verdict. Destructive suggestions never accept bare Enter — an explicit
   typed `y` is required after a one-line warning. Low-confidence suggestions
   (below the configurable threshold) get the same strict prompt plus a
   "best guess" label.
4. **Nothing executes without confirmation**, and the executed command's exit
   code is propagated. Edited commands (`e`) are re-checked and re-confirmed.
5. **Loopback enforcement:** for the local providers (ollama, custom) the
   endpoint must resolve to loopback unless the user explicitly sets
   `allow_non_local = true`. Nothing leaves the machine by default, ever;
   selecting a cloud provider in config is the one explicit opt-out (see
   Providers above).

**`shell_state` correction:** small models set `shell_state` unreliably (the
1.5B model flagged `git push` and `mkdir test && git init` as shell-state
during development). The model's hint is therefore **ignored for control
flow** — it stays in the JSON contract but never blocks execution. The
deterministic detector (cd/export/source/activate/`$env:` prefixes) is the
sole authority, and it exists for mechanics, not policy: those commands are
a silent no-op in a child process (a subprocess `cd` cannot move the user's
shell), so running them would lie to the user. A confirmed command that the
detector does not match always runs. The failure mode of a detector miss is
mild — the command runs in a child and visibly has no effect — while the
failure mode of a false positive is a confirmed command that refuses to run.

## Shell integration tradeoffs

We deliberately did **not** build a shell, REPL wrapper, or PTY proxy — that
path breaks completions, keybindings, and prompts, and means reimplementing
every shell's line editor. Instead, the zoxide/starship/thefuck pattern:

1. **`nudge <words>`** — plain binary invocation. Works everywhere including
   cmd.exe, zero setup. But it cannot see your last command (shell history is
   shell state), and standalone execution runs suggestions in a *subprocess*,
   so `cd`/`activate` suggestions can't take effect — those are printed with a
   note instead. When the integration *is* installed, the wrapper function
   routes explicit invocations through `--shell-eval` too, so shell-state
   suggestions take effect; subcommands (`init`, `doctor`, `setup`, …) are
   passed through directly so their stdout is never eval'd.
2. **Bare `nudge` / `fix`** — a shell *function* installed by `nudge init`
   that shadows the binary. Called with no args it passes the last few history
   lines (bash/zsh `fc -ln -5`, PowerShell `Get-History`, fish `$history`) via
   the `NUDGE_HISTORY` env var, plus `$?`/`$LASTEXITCODE`, and evals whatever
   the binary prints on stdout. It must be a function: bash doesn't flush
   history to disk until the session ends, so no standalone binary can
   reliably read it. The binary filters out its own invocations from the
   history sample (bash includes the in-flight command; PowerShell doesn't).
   This is the only integration that catches "binary exists but invocation
   failed" (`dotnet create migrations`) — no shell hook fires for a nonzero
   exit. The eval-stdout channel is also what makes shell-state suggestions
   actually work: the confirmed command is printed to stdout and executed by
   the *shell*, not a child process. All human interaction happens on
   stderr/stdin so stdout stays clean for eval.
3. **command-not-found hooks** (`command_not_found_handle(r)`,
   `fish_command_not_found`, PowerShell `CommandNotFoundAction`) fire
   automatically — but *only* when the binary itself doesn't exist. That
   limitation is documented rather than papered over; `fix` covers the rest.
   The PowerShell hook filters on `CommandOrigin -eq 'Runspace'` (interactive
   lookups only) and skips PowerShell's internal `get-` retry probe. The
   PowerShell and fish hooks run in the shell process itself, so they route
   through `--shell-eval` and eval the output — shell-state corrections take
   effect. In bash/zsh the handler runs in a forked child that cannot touch
   the parent shell, so those hooks execute corrections as subprocesses —
   shell-state corrections there still need `fix`.

Execution semantics summary: through the wrapper (bare *or* explicit) →
confirmed command goes to stdout, shell evals it; for PowerShell targets,
`&&` chains are rewritten to `$?`-guarded blocks first (5.1 can't parse
`&&`; the guarded form is equivalent on 7). Standalone → subprocess (same
rewrite when falling back to powershell.exe), except shell-state suggestions
which are printed with a note. Non-TTY → print the suggestion, exit 3,
never prompt, never run.

## Decisions made along the way

- **Module path** is plain `nudge` (not published to a forge yet); rename on
  first release.
- **Exit codes:** propagate the child's on success; `1` = no suggestion /
  declined / error; `3` = non-TTY, suggestion printed; `2` = usage error.
- **Edit prompt** is print-and-retype (Enter keeps the line): true inline
  prefill needs raw-mode/readline, which fails the "no heavy deps" bar.
  Revisit if a tiny prefill trick proves portable, especially on Windows.
- **`NUDGE_FORCE_INTERACTIVE=1`** is a documented test hook that bypasses TTY
  detection so CI (and this project's own acceptance script) can drive the
  confirm flow through pipes.
- **Windows console ANSI**: enabled via `SetConsoleMode`
  (`ENABLE_VIRTUAL_TERMINAL_PROCESSING`) at startup; if that fails (legacy
  conhost), output falls back to plain text. `NO_COLOR` is honored.
- **Anagram half-step scoring** in Tier 1 (see above) came out of a real
  collision: promoting anagrams to distance 1 made `gti` ambiguous between
  `git` and `tig` on a machine that had both installed.
- **Future work:** stderr capture for richer fix-mode context (needs a shell
  hook that tees stderr, e.g. a `preexec` wrapper — invasive, deferred);
  Scoop/Homebrew packaging; multi-suggestion ranking (top-3) once small
  models can rank reliably.
