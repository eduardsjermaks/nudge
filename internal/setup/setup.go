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
	} else if !confirmBrain() {
		return 1
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

	if code != 0 {
		ui.Errf("\nNot everything is ready — run %s again: it is safe to re-run and\n", ui.Bold("nudge setup"))
		ui.Errf("often completes on the second pass (a server or download started in the\n")
		ui.Errf("background may just have needed more time than the wizard waited).\n")
	}
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
		// Written out even though ollama is the built-in default, so a
		// switch away from a cloud provider actually takes effect.
		return writeConfigLines([]string{`provider = "ollama"`})
	default:
		ui.Errf("unrecognized choice %q\n", ans)
		return false
	}
}

// confirmBrain runs when a config already exists: re-running setup is also
// how the provider gets switched — there is no separate reconfigure command
// — so show what is active and offer the choice again.
func confirmBrain() bool {
	cfg, err := config.Load()
	if err != nil {
		return true // the main Load in Run reports the error
	}
	name := cfg.Provider
	if name == "" {
		name = "ollama"
	}
	keep, err := ui.AskYesNo(fmt.Sprintf("Current provider: %s. Keep it?", ui.Bold(name)), true)
	if err != nil {
		return false
	}
	if keep {
		maybeUpdateKey(cfg)
		return true
	}
	ui.Errf("  (choosing again rewrites %s — custom settings in it are lost)\n", config.Path())
	return chooseBrain()
}

// cloudProviders maps each provider to its standard credential env var and
// the portal page where a key is created.
var cloudProviders = map[string]struct {
	keyEnv string
	portal string
}{
	"openai":    {"OPENAI_API_KEY", "https://platform.openai.com/api-keys"},
	"anthropic": {"ANTHROPIC_API_KEY", "https://console.anthropic.com/settings/keys"},
	"deepseek":  {"DEEPSEEK_API_KEY", "https://platform.deepseek.com/api_keys"},
	"azure":     {"AZURE_OPENAI_API_KEY", "https://portal.azure.com — your Azure OpenAI resource, \"Keys and Endpoint\""},
}

// cloudSetup writes a minimal config.toml for a cloud provider. The
// provider's standard env var is checked first; when it is not set, the
// wizard offers to store a pasted key as api_key in the config file (kept
// private via 0600 on POSIX; the env var still overrides it).
func cloudSetup() bool {
	name, err := ui.Ask("Which provider? [openai/anthropic/deepseek/azure]:")
	if err != nil {
		return false
	}
	name = strings.ToLower(name)
	p, ok := cloudProviders[name]
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

	keyStored := false
	if os.Getenv(p.keyEnv) == "" {
		ui.Errf("  %s needs an API key (read from the %s env variable).\n", ui.Bold(name), ui.Bold(p.keyEnv))
		ui.Errf("  Create or copy one here:\n")
		ui.Errf("    %s\n", ui.Bold(p.portal))
		ui.Errf("  Paste it below to save it in nudge's config file (readable only by\n")
		ui.Errf("  you), or press Enter to skip and set %s yourself later.\n", p.keyEnv)
		key, err := ui.Ask("API key (Enter = skip):")
		if err != nil {
			return false
		}
		if key = strings.TrimSpace(key); key != "" {
			lines = append(lines, fmt.Sprintf("api_key = %q", key))
			keyStored = true
		}
	}

	if !writeConfigLines(lines) {
		return false
	}

	switch {
	case keyStored:
		ui.Errf("  %s API key saved (setting the %s env variable later would override it)\n", ui.Cyan("ok"), p.keyEnv)
	case os.Getenv(p.keyEnv) != "":
		ui.Errf("  %s %s is already set in this environment\n", ui.Cyan("ok"), p.keyEnv)
	default:
		ui.Errf("  skipped — set your API key in the %s environment variable:\n", ui.Bold(p.keyEnv))
		if runtime.GOOS == "windows" {
			ui.Errf("    [Environment]::SetEnvironmentVariable('%s', '<your key>', 'User')\n", p.keyEnv)
			ui.Errf("    (then open a new terminal and re-run `nudge doctor`)\n")
		} else {
			ui.Errf("    export %s='<your key>'   # add to your shell rc to persist\n", p.keyEnv)
		}
	}
	return true
}

// maybeUpdateKey lets a re-run change the credential for a kept cloud
// provider — without this, setup could store a key once but never fix a
// wrong or rotated one.
func maybeUpdateKey(cfg config.Config) {
	p, ok := cloudProviders[cfg.Provider]
	if !ok {
		return
	}
	change, err := ui.AskYesNo("Change the API key?", false)
	if err != nil || !change {
		return
	}
	if os.Getenv(p.keyEnv) != "" {
		ui.Errf("  the active key comes from the %s environment variable, which\n", ui.Bold(p.keyEnv))
		ui.Errf("  nudge cannot change. Update it there:\n")
		if runtime.GOOS == "windows" {
			ui.Errf("    [Environment]::SetEnvironmentVariable('%s', '<new key>', 'User')\n", p.keyEnv)
			ui.Errf("    (then open a new terminal)\n")
		} else {
			ui.Errf("    export %s='<new key>'   # update your shell rc\n", p.keyEnv)
		}
		return
	}
	ui.Errf("  Create or copy a key: %s\n", ui.Bold(p.portal))
	key, err := ui.Ask("New API key (Enter = cancel):")
	if err != nil {
		return
	}
	if key = strings.TrimSpace(key); key == "" {
		ui.Errf("  unchanged.\n")
		return
	}
	if err := setConfigKey(config.Path(), key); err != nil {
		ui.Errf("  cannot update %s: %v\n", config.Path(), err)
		return
	}
	ui.Errf("  %s API key updated\n", ui.Cyan("ok"))
}

// setConfigKey replaces (or adds) the api_key line in config.toml, leaving
// every other setting untouched.
func setConfigKey(path, key string) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var out []string
	for _, line := range strings.Split(strings.TrimRight(string(b), "\n"), "\n") {
		t := strings.TrimSpace(line)
		// exact key only — "api_key_env" must survive
		if strings.HasPrefix(t, "api_key ") || strings.HasPrefix(t, "api_key=") {
			continue
		}
		out = append(out, line)
	}
	out = append(out, fmt.Sprintf("api_key = %q", key))
	return os.WriteFile(path, []byte(strings.Join(out, "\n")+"\n"), 0o600)
}

// writeConfigLines replaces config.toml with the given lines. 0600 because
// the file may hold an API key.
func writeConfigLines(lines []string) bool {
	path := config.Path()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		ui.Errf("cannot create %s: %v\n", filepath.Dir(path), err)
		return false
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		ui.Errf("cannot write %s: %v\n", path, err)
		return false
	}
	ui.Errf("  %s wrote %s\n", ui.Cyan("ok"), path)
	return true
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
