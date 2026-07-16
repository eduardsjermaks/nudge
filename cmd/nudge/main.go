// Command nudge proposes the command you meant. Tier 1 fixes pure typos
// instantly from names harvested off the machine; Tier 2 asks a local LLM.
// Nothing runs without confirmation; nothing leaves the machine.
package main

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"nudge/internal/config"
	"nudge/internal/doctor"
	"nudge/internal/execx"
	"nudge/internal/llm"
	"nudge/internal/mask"
	"nudge/internal/prompt"
	"nudge/internal/provider"
	"nudge/internal/safety"
	"nudge/internal/setup"
	"nudge/internal/shell"
	"nudge/internal/suggest"
	"nudge/internal/tier1"
	"nudge/internal/ui"
)

var version = "0.1.0"

const (
	exitNoSuggestion   = 1
	exitNonInteractive = 3
)

type opts struct {
	explain   bool
	shellEval bool
	notFound  bool
	fixMode   bool
	lastExit  int
	shell     string // set by the wrapper (--shell); "" = autodetect
}

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) > 0 {
		switch args[0] {
		case "init":
			if len(args) < 2 {
				ui.Errf("usage: nudge init <bash|zsh|fish|powershell>\n")
				return 2
			}
			snippet, err := shell.Init(args[1])
			if err != nil {
				ui.Errf("nudge: %v\n", err)
				return 2
			}
			fmt.Print(snippet)
			return 0
		case "setup":
			return setup.Run()
		case "doctor":
			return doctor.Run()
		case "version", "--version", "-v":
			fmt.Printf("nudge %s\n", version)
			return 0
		case "help", "--help", "-h":
			usage()
			return 0
		}
	}

	var o opts
	words := parseFlags(args, &o)
	if o.notFound {
		o.fixMode = true
		if o.lastExit == 0 {
			o.lastExit = 127
		}
	}

	// Bare `nudge` via the shell wrapper: the failed command comes from
	// history (NUDGE_HISTORY is set by the init snippet).
	if len(words) == 0 {
		hist := os.Getenv("NUDGE_HISTORY")
		if hist == "" {
			ui.Errf("nudge: nothing to fix. Type `nudge <the command you meant>`,\n")
			ui.Errf("or install the shell integration so bare `nudge` (and `fix`) can read your last command:\n")
			ui.Errf("  %s\n", initHint())
			return exitNoSuggestion
		}
		last := shell.PickLastCommand(hist)
		if last == "" {
			ui.Errf("nudge: could not find a previous command in shell history\n")
			return exitNoSuggestion
		}
		words = strings.Fields(last)
		o.fixMode = true
	}

	return correct(o, words)
}

// parseFlags strips nudge's own flags wherever they appear (so trailing
// `--explain` works); everything after a literal `--` is input verbatim.
func parseFlags(args []string, o *opts) []string {
	var words []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--explain":
			o.explain = true
		case "--shell-eval":
			o.shellEval = true
		case "--not-found":
			o.notFound = true
		case "--fix":
			o.fixMode = true
		case "--last-exit":
			if i+1 < len(args) {
				i++
				if n, err := strconv.Atoi(args[i]); err == nil && n > 0 {
					o.lastExit = n
				}
			}
		case "--shell":
			if i+1 < len(args) {
				i++
				o.shell = strings.ToLower(args[i])
			}
		case "--":
			return append(words, args[i+1:]...)
		default:
			words = append(words, args[i])
		}
	}
	return words
}

func correct(o opts, words []string) int {
	interactive := ui.IsTTY(os.Stdin) && ui.IsTTY(os.Stderr)
	if !o.shellEval {
		interactive = interactive && ui.IsTTY(os.Stdout)
	}
	if os.Getenv("NUDGE_FORCE_INTERACTIVE") == "1" {
		interactive = true // test hook: lets CI drive the prompt through pipes
	}
	input := strings.Join(words, " ")

	// --- Tier 1: derived typo fixer ---
	t0 := time.Now()
	s := tier1.Match(words)
	if !o.fixMode {
		// Without an explicit mode, treat input as a failed command when
		// tier 1 corrected a typo in it, or when its first word is a real
		// executable; otherwise as plain-words intent.
		o.fixMode = s != nil || isExecutable(words[0])
	}
	tier1Took := time.Since(t0)
	if s != nil {
		if o.explain {
			ui.Errf("%s\n", ui.Dim(fmt.Sprintf("[explain] tier 1 (derived typo fixer) answered in %s — no model call", tier1Took.Round(time.Millisecond))))
		}
		return present(o, input, s, config.Defaults().Confidence, interactive)
	}
	if o.explain {
		ui.Errf("%s\n", ui.Dim(fmt.Sprintf("[explain] tier 1: no confident match (%s), asking the model", tier1Took.Round(time.Millisecond))))
	}

	// --- Tier 2: the configured model provider ---
	cfg, err := config.Load()
	if err != nil {
		ui.Errf("nudge: %v\n", err)
		return exitNoSuggestion
	}
	prov, err := provider.Resolve(cfg)
	if err != nil {
		ui.Errf("nudge: %v\n", err)
		return exitNoSuggestion
	}
	if err := prov.KeyError(); err != nil {
		ui.Errf("nudge: %v\n", err)
		return exitNoSuggestion
	}

	client := llm.New(cfg, prov)
	req := prompt.Request{
		Input:    input,
		FixMode:  o.fixMode,
		ExitCode: o.lastExit,
		Dir:      cwd(),
		Shell:    o.shell,
	}
	// Cloud providers get a masked copy of the input; the placeholders are
	// swapped back after the model answers. Local providers see the input
	// as-is.
	var secrets map[string]string
	if prov.Cloud {
		req.Input, secrets = mask.Mask(input)
	}

	var sp *ui.Spinner
	if interactive {
		sp = ui.StartSpinner("thinking...")
	}
	t0 = time.Now()
	s, err = askModel(client, prov.Timeout, req)
	tier2Took := time.Since(t0)
	if sp != nil {
		sp.Stop()
	}
	if err != nil {
		if isConnErr(err) {
			if prov.Cloud {
				ui.Errf("nudge: %s unreachable — check your network; typo fixes (tier 1) still work.\n", prov.Name)
			} else {
				ui.Errf("nudge: local model server unreachable at %s — typo fixes (tier 1) still work.\n", cfg.Endpoint)
			}
			ui.Errf("run %s to diagnose.\n", ui.Bold("nudge doctor"))
		} else {
			ui.Errf("nudge: %v\n", err)
		}
		return exitNoSuggestion
	}
	if len(secrets) > 0 {
		s.Command = mask.Restore(s.Command, secrets)
		s.Explanation = mask.Restore(s.Explanation, secrets)
	}
	if o.explain {
		ui.Errf("%s\n", ui.Dim(fmt.Sprintf("[explain] tier 2 (%s: %s) answered in %s, confidence %.2f", prov.Name, prov.Model, tier2Took.Round(10*time.Millisecond), s.Confidence)))
	}
	if s.Command == "" || s.Confidence == 0 {
		ui.Errf("nudge: no suggestion — I can't tell what you meant by `%s`.\n", input)
		return exitNoSuggestion
	}
	return present(o, input, s, cfg.Confidence, interactive)
}

// askModel calls the model, validating hard; one retry on invalid JSON.
func askModel(client llm.Client, timeout time.Duration, req prompt.Request) (*suggest.Suggestion, error) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		raw, err := client.Chat(ctx, prompt.System, req.User())
		if err != nil {
			return nil, err
		}
		s, err := suggest.Parse(raw)
		if err == nil {
			return s, nil
		}
		lastErr = err
	}
	return nil, fmt.Errorf("model kept returning unusable output (%v) — no suggestion", lastErr)
}

// present shows the suggestion, handles placeholders, safety, confirmation,
// and execution. Returns the process exit code.
func present(o opts, input string, s *suggest.Suggestion, threshold float64, interactive bool) int {
	lowConf := s.Source == suggest.SourceTier2 && s.Confidence < threshold

	if !interactive {
		// Non-TTY: print the suggestion, never prompt, never run.
		fmt.Println(s.Display())
		return exitNonInteractive
	}

	if o.fixMode && input != "" {
		ui.Errf("%s isn't a valid command. Did you mean:\n", ui.Bold("`"+input+"`"))
	} else {
		ui.Errf("Did you mean:\n")
	}
	expl := s.Explanation
	if expl != "" {
		expl = "    " + ui.Dim("("+expl+")")
	}
	ui.Errf("  %s %s%s\n", ui.Cyan("→"), ui.Bold(s.Display()), expl)
	if lowConf {
		ui.Errf("  %s\n", ui.Yellow("best guess — double-check the flags"))
	}

	// Placeholders are filled before the confirm step so the user always
	// sees exactly what will run.
	final := s.Command
	if len(s.Placeholders) > 0 {
		values := map[string]string{}
		for _, name := range s.Placeholders {
			v, err := ui.AskValue(name)
			if err != nil {
				return exitNoSuggestion
			}
			values[name] = v
		}
		final = s.Fill(values)
		ui.Errf("  %s %s\n", ui.Dim("will run:"), ui.Bold(final))
	}

	for {
		verdict := safety.Check(final, s.Destructive)
		if verdict.Destructive {
			ui.Errf("  %s\n", ui.Red("! destructive: "+verdict.Reason+" — requires an explicit 'y'"))
		}
		strict := verdict.Destructive || lowConf

		d, err := ui.Confirm(strict)
		if err != nil {
			return exitNoSuggestion
		}
		switch d {
		case ui.No:
			ui.Errf("aborted.\n")
			return exitNoSuggestion
		case ui.Edit:
			final, err = ui.EditLine(final)
			if err != nil {
				return exitNoSuggestion
			}
			continue // re-check safety on the edited line, confirm again
		case ui.Run:
			return execute(o, final, s)
		}
	}
}

func execute(o opts, final string, s *suggest.Suggestion) int {
	if o.shellEval {
		// The wrapper function evals stdout in the user's shell, so
		// shell-state suggestions (cd, activate) work naturally. PowerShell
		// gets && chains rewritten: 5.1 cannot parse them, and the guarded
		// form is equivalent on 7. The wrapper's --shell is authoritative —
		// env heuristics misfire when shells nest (pwsh under Git Bash
		// inherits SHELL, Git Bash under Windows sees PSModulePath).
		sh := o.shell
		if sh == "" {
			sh = prompt.ShellName()
		}
		if sh == "powershell" || sh == "pwsh" {
			final = execx.RewriteAndChains(final)
		}
		fmt.Println(final)
		return 0
	}
	if safety.ChangesShellState(final) {
		fmt.Println(final)
		ui.Errf("this command only takes effect inside your shell — run the line above yourself.\n")
		ui.Errf("(bare `nudge` / `fix` after a failure applies it for you; needs the integration: %s)\n", initHint())
		return 0
	}
	return execx.Run(final)
}

func isExecutable(word string) bool {
	w := strings.ToLower(word)
	exes := tier1.Executables()
	i := sort.SearchStrings(exes, w)
	return i < len(exes) && exes[i] == w
}

func isConnErr(err error) bool {
	msg := err.Error()
	return strings.Contains(msg, "connection refused") ||
		strings.Contains(msg, "connectex") ||
		strings.Contains(msg, "no such host") ||
		strings.Contains(msg, "context deadline exceeded") ||
		strings.Contains(msg, "connection reset")
}

func cwd() string {
	d, err := os.Getwd()
	if err != nil {
		return "."
	}
	return d
}

func initHint() string {
	sh := prompt.ShellName()
	switch sh {
	case "powershell", "cmd":
		return `add  Invoke-Expression (& nudge init pwsh | Out-String)  to your $PROFILE`
	case "fish":
		return `add  nudge init fish | source  to ~/.config/fish/config.fish`
	case "zsh":
		return `add  eval "$(nudge init zsh)"  to ~/.zshrc`
	default:
		return `add  eval "$(nudge init bash)"  to ~/.bashrc`
	}
}

func usage() {
	fmt.Print(`nudge — proposes the command you meant, using a local LLM by default (nothing
leaves your machine unless you configure a cloud provider).

usage:
  nudge <failed command or plain words>   suggest a correction / a command
  nudge                                   fix the last command (needs shell integration)
  nudge setup                             interactive wizard: model server, model, shell hook
  nudge init <bash|zsh|fish|powershell>   print the shell integration snippet
  nudge doctor                            diagnose the active model provider
  nudge version                           print version

Shell integration (enables bare nudge and fix; nudge must be on PATH):
PowerShell: New-Item -ItemType File -Path $PROFILE -Force; Add-Content -Path $PROFILE -Value 'Invoke-Expression (& nudge init pwsh | Out-String)'; . $PROFILE
bash:       Add: echo 'eval "$(nudge init bash)"' >> ~/.bashrc; then run: source ~/.bashrc
zsh:        Add: echo 'eval "$(nudge init zsh)"' >> ~/.zshrc; then run: source ~/.zshrc
fish:       mkdir -p ~/.config/fish; nudge init fish >> ~/.config/fish/config.fish; source ~/.config/fish/config.fish

flags:
  --explain      show which tier answered and how long it took
  --last-exit N  exit code of the failed command (used by the shell wrapper)
  --fix          force fix mode (treat input as a failed command)

exit codes:
  (run)  exit code of the executed command
  1      no suggestion, or aborted
  3      not a TTY: suggestion printed, nothing executed
`)
}
