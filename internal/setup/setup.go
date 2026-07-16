// Package setup implements `nudge setup`: an interactive first-run wizard
// that takes a fresh binary to a working state — model server installed and
// running, model pulled, shell integration in place — asking before every
// action that changes the machine. Every step detects "already done", so the
// wizard is safe to re-run at any time. Steps are best-effort: a skipped or
// failed step is reported and the closing doctor run decides the exit code.
package setup

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"nudge/internal/config"
	"nudge/internal/doctor"
	"nudge/internal/ui"
)

const exitNonInteractive = 3

// Run executes the wizard and returns a process exit code.
func Run() int {
	interactive := ui.IsTTY(os.Stdin) && ui.IsTTY(os.Stderr)
	if os.Getenv("NUDGE_FORCE_INTERACTIVE") == "1" {
		interactive = true // test hook, same as the confirm prompt
	}
	if !interactive {
		ui.Errf("nudge setup is interactive and needs a terminal.\n")
		ui.Errf("For scripted installs follow the manual steps:\n")
		ui.Errf("  https://github.com/eduardsjermaks/nudge#install-5-minutes\n")
		return exitNonInteractive
	}

	ui.Errf("%s — checks each piece and offers to fix what's missing.\n", ui.Bold("nudge setup"))
	ui.Errf("Nothing changes without your confirmation. Re-run it any time.\n\n")

	if !fileExists(config.Path()) {
		if !chooseBrain() {
			return 1
		}
	}

	cfg, err := config.Load()
	if err != nil {
		ui.Errf("nudge: %v\n", err)
		return 1
	}

	switch cfg.Provider {
	case "", "ollama":
		ensureOllama(cfg)
	default:
		ui.Errf("  provider %s configured in %s — no local server to manage.\n",
			ui.Bold(cfg.Provider), config.Path())
	}

	reload := ensureIntegration()

	ui.Errf("\nchecking the result (%s):\n", ui.Bold("nudge doctor"))
	code := doctor.Run()

	if reload != "" {
		ui.Errf("\n%s the shell hook was added to your profile, but this session\n", ui.Bold("one last step:"))
		ui.Errf("started without it — misspelled commands won't be caught here yet. Run:\n")
		ui.Errf("  %s\n", ui.Bold(reload))
		ui.Errf("or open a new terminal.\n")
	}
	return code
}

// chooseBrain runs only when no config file exists yet. Cloud is the
// recommended first choice and writes a minimal config.toml; local Ollama
// needs no config at all (it is the built-in provider default).
func chooseBrain() bool {
	ui.Errf("Where should suggestions come from? (typo fixes never use a model)\n")
	ui.Errf("  1. %s — best quality, no local install; needs an API key, queries leave this machine (recommended)\n", ui.Bold("cloud"))
	ui.Errf("  2. %s — private, free per query, ~1 GB model download\n", ui.Bold("local (Ollama)"))
	ans, err := ui.Ask("Choose [1/2, Enter = 1]:")
	if err != nil {
		return false
	}
	switch ans {
	case "", "1":
		return cloudSetup()
	case "2":
		return true
	default:
		ui.Errf("unrecognized choice %q\n", ans)
		return false
	}
}

// cloudSetup writes a minimal config.toml for a cloud provider. It never
// asks for the key itself — the documented, preferred place for credentials
// is the provider's standard environment variable.
func cloudSetup() bool {
	keyEnvs := map[string]string{
		"openai":    "OPENAI_API_KEY",
		"anthropic": "ANTHROPIC_API_KEY",
		"deepseek":  "DEEPSEEK_API_KEY",
		"azure":     "AZURE_OPENAI_API_KEY",
	}
	name, err := ui.Ask("Which provider? [openai/anthropic/deepseek/azure]:")
	if err != nil {
		return false
	}
	name = strings.ToLower(name)
	keyEnv, ok := keyEnvs[name]
	if !ok {
		ui.Errf("unknown provider %q\n", name)
		return false
	}

	lines := []string{fmt.Sprintf("provider = %q", name)}
	if name == "azure" {
		ep, err := ui.Ask("Azure endpoint (https://<resource>.openai.azure.com):")
		if err != nil || ep == "" {
			return false
		}
		dep, err := ui.Ask("Azure deployment name:")
		if err != nil || dep == "" {
			return false
		}
		lines = append(lines,
			fmt.Sprintf("azure_endpoint = %q", strings.TrimRight(ep, "/")),
			fmt.Sprintf("azure_deployment = %q", dep))
	}

	path := config.Path()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		ui.Errf("cannot create %s: %v\n", filepath.Dir(path), err)
		return false
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		ui.Errf("cannot write %s: %v\n", path, err)
		return false
	}
	ui.Errf("  %s wrote %s\n", ui.Cyan("ok"), path)

	if os.Getenv(keyEnv) != "" {
		ui.Errf("  %s %s is already set in this environment\n", ui.Cyan("ok"), keyEnv)
	} else {
		ui.Errf("  now set your API key in the %s environment variable:\n", ui.Bold(keyEnv))
		if runtime.GOOS == "windows" {
			ui.Errf("    [Environment]::SetEnvironmentVariable('%s', '<your key>', 'User')\n", keyEnv)
			ui.Errf("    (then open a new terminal and re-run `nudge doctor`)\n")
		} else {
			ui.Errf("    export %s='<your key>'   # add to your shell rc to persist\n", keyEnv)
		}
	}
	return true
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
