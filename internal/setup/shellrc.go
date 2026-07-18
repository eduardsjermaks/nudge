package setup

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"nudge/internal/prompt"
	"nudge/internal/ui"
)

// integrationMarker is the substring that proves the hook is already
// installed — every shell's integration line contains it.
const integrationMarker = "nudge init"

type integration struct {
	rc     string // absolute path of the rc/profile file
	line   string // the line to append
	reload string // command the user runs to activate it now
	psExe  string // PowerShell executable; set only for the PowerShell hook
}

// ensureIntegration offers to add the shell hook (bare `nudge` / `fix`,
// command-not-found catch) to the user's rc file. Idempotent: an existing
// hook is detected and left alone. Returns the reload command when the hook
// was added just now — the running session started without it, so the caller
// must surface the reload step at the very end of the wizard.
func ensureIntegration() (reload string) {
	integ, err := detectIntegration()
	if err != nil {
		ui.Errf("  shell integration: %v — see the README for manual steps.\n", err)
		return ""
	}
	// A restrictive execution policy silently defeats the whole hook: the
	// profile line gets added but every new session errors instead of
	// loading it. Fix that first.
	if integ.psExe != "" {
		ensurePSPolicy(integ.psExe)
	}

	present, err := fileContains(integ.rc, integrationMarker)
	if err != nil {
		ui.Errf("  shell integration: cannot read %s: %v\n", integ.rc, err)
		return ""
	}
	if present {
		ui.Errf("  %s shell integration already present in %s\n", ui.Cyan("ok"), integ.rc)
		return ""
	}

	ui.Errf("  Shell integration enables bare %s / %s after a failed command\n", ui.Bold("nudge"), ui.Bold("fix"))
	ui.Errf("  and catches misspelled binaries automatically. It appends one line:\n")
	ui.Errf("    %s\n", ui.Bold(integ.line))
	yes, err := ui.AskYesNo(fmt.Sprintf("  Add it to %s?", integ.rc), true)
	if err != nil || !yes {
		ui.Errf("  skipped.\n")
		return ""
	}
	if err := appendLine(integ.rc, integ.line); err != nil {
		ui.Errf("  failed to update %s: %v\n", integ.rc, err)
		return ""
	}
	ui.Errf("  %s added\n", ui.Cyan("ok"))
	return integ.reload
}

func detectIntegration() (*integration, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	switch sh := prompt.ShellName(); sh {
	case "powershell", "cmd": // cmd users get the PowerShell hook, same as initHint
		profile, exe, err := pwshProfile()
		if err != nil {
			return nil, fmt.Errorf("cannot determine $PROFILE: %v", err)
		}
		return &integration{
			rc:     profile,
			line:   "Invoke-Expression (& nudge init pwsh | Out-String)",
			reload: ". $PROFILE",
			psExe:  exe,
		}, nil
	case "zsh":
		return &integration{
			rc:     filepath.Join(home, ".zshrc"),
			line:   `eval "$(nudge init zsh)"`,
			reload: "source ~/.zshrc",
		}, nil
	case "fish":
		return &integration{
			rc:     filepath.Join(home, ".config", "fish", "config.fish"),
			line:   "nudge init fish | source",
			reload: "source ~/.config/fish/config.fish",
		}, nil
	case "bash", "sh":
		return &integration{
			rc:     filepath.Join(home, ".bashrc"),
			line:   `eval "$(nudge init bash)"`,
			reload: "source ~/.bashrc",
		}, nil
	default:
		return nil, fmt.Errorf("unsupported shell %q", sh)
	}
}

// pwshProfile asks PowerShell itself for $PROFILE — the path depends on the
// PowerShell edition (5.1 vs 7) and on Documents redirection (OneDrive), so
// computing it from Go would guess wrong. Also returns which executable
// answered, so later checks (execution policy) talk to the same edition.
func pwshProfile() (profile, psExe string, err error) {
	// PS7 puts ...\PowerShell\7\Modules on PSModulePath; 5.1 never does.
	exes := []string{"powershell", "pwsh"}
	if strings.Contains(strings.ToLower(os.Getenv("PSModulePath")), `\powershell\7\`) {
		exes = []string{"pwsh", "powershell"}
	}
	lastErr := errors.New("no PowerShell executable found")
	for _, e := range exes {
		out, err := exec.Command(e, "-NoProfile", "-Command", "Write-Output $PROFILE").Output()
		if err != nil {
			lastErr = err
			continue
		}
		// PowerShell 5.1 may emit a UTF-8 BOM on redirected output.
		p := strings.TrimSpace(strings.TrimPrefix(string(out), "\ufeff"))
		if p != "" {
			return p, e, nil
		}
	}
	return "", "", lastErr
}

// ensurePSPolicy offers to relax a PowerShell execution policy that would
// block profile scripts. Windows PowerShell 5.1 ships Restricted on client
// machines, so without this a fresh install errors with UnauthorizedAccess
// on every new session once the profile line exists.
func ensurePSPolicy(exe string) {
	out, err := exec.Command(exe, "-NoProfile", "-Command", "Get-ExecutionPolicy").Output()
	if err != nil {
		return
	}
	pol := strings.TrimSpace(strings.TrimPrefix(string(out), "\ufeff"))
	if !policyBlocksProfiles(pol) {
		return
	}
	ui.Errf("  PowerShell's execution policy is %s — it blocks profile scripts, so\n", ui.Bold(pol))
	ui.Errf("  the integration cannot load (\"running scripts is disabled\" errors).\n")
	ui.Errf("  RemoteSigned allows local scripts and is Microsoft's recommended default.\n")
	yes, err := ui.AskYesNo("  Set it for your user account (Set-ExecutionPolicy -Scope CurrentUser RemoteSigned)?", true)
	if err != nil || !yes {
		ui.Errf("  skipped — the integration will not load until you run:\n")
		ui.Errf("    Set-ExecutionPolicy -Scope CurrentUser RemoteSigned\n")
		return
	}
	if err := exec.Command(exe, "-NoProfile", "-Command",
		"Set-ExecutionPolicy -Scope CurrentUser -ExecutionPolicy RemoteSigned -Force").Run(); err != nil {
		ui.Errf("  failed to set it (a group policy may enforce the current value): %v\n", err)
		return
	}
	ui.Errf("  %s execution policy set to RemoteSigned for your user\n", ui.Cyan("ok"))
}

// policyBlocksProfiles reports whether an effective execution policy stops
// $PROFILE from loading. Pure so tests can cover the matrix.
func policyBlocksProfiles(policy string) bool {
	switch policy {
	case "Restricted", "AllSigned", "Undefined":
		return true
	}
	return false
}

func fileContains(path, needle string) (bool, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return strings.Contains(string(b), needle), nil
}

// appendLine appends line to path, creating parent directories and the file
// as needed, and never truncating existing content.
func appendLine(path, line string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	var prefix string
	if len(b) > 0 && !bytes.HasSuffix(b, []byte("\n")) {
		prefix = "\n"
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(prefix + line + "\n")
	return err
}
