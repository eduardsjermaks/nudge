// Package safety is the deterministic guardrail that decides whether a
// suggested command is destructive. It is a fixed, hardcoded detector — a
// safety property of the tool, not matching configuration. The model's own
// destructive flag is only a hint; this detector always runs and always wins
// in the dangerous direction (it can escalate a suggestion to destructive,
// never downgrade one).
package safety

import (
	"regexp"
	"strings"
)

type Verdict struct {
	Destructive bool
	Reason      string
}

type rule struct {
	re     *regexp.Regexp
	reason string
}

var rules = []rule{
	{regexp.MustCompile(`(?i)\brm\s+(-[a-z]*\s+)*-[a-z]*[rf][a-z]*\b`), "recursive/forced file deletion (rm)"},
	{regexp.MustCompile(`(?i)\brm\s+-[a-z]*\s+-[a-z]*\b`), "forced file deletion (rm with multiple flags)"},
	{regexp.MustCompile(`(?i)\bgit\s+.*--hard\b`), "git --hard discards local changes"},
	{regexp.MustCompile(`(?i)\bgit\s+push\b.*(--force\b|--force-with-lease\b|\s-f\b)`), "force push rewrites remote history"},
	{regexp.MustCompile(`(?i)\bgit\s+clean\b`), "git clean deletes untracked files"},
	{regexp.MustCompile(`(?i)\bcheckout\s+--\s`), "git checkout -- discards file changes"},
	{regexp.MustCompile(`(?i)\bprune\b`), "prune permanently removes data"},
	{regexp.MustCompile(`(?i)\b(drop|truncate)\s+(table|database|schema|index|column|collection)\b`), "destructive database statement"},
	{regexp.MustCompile(`(?i)\bdd\s+.*\bof=`), "dd writing to a device/file"},
	{regexp.MustCompile(`(?i)\bmkfs(\.[a-z0-9]+)?\b`), "filesystem format"},
	{regexp.MustCompile(`(?i)(^|[\s;&|])format(\.com)?\s+[a-z]:`), "drive format"},
	{regexp.MustCompile(`(?i)\bdel\s+(/[a-z]+\s+)*/s\b`), "recursive delete (del /s)"},
	{regexp.MustCompile(`(?i)\brd\s+/s\b|\brmdir\s+/s\b`), "recursive directory removal"},
	{regexp.MustCompile(`(?i)remove-item\b.*(-recurse|-force)`), "Remove-Item -Recurse/-Force"},
	{regexp.MustCompile(`(?i)\b(docker|podman)\s+(system|volume|image|container|network)?\s*prune\b`), "docker prune removes data"},
	{regexp.MustCompile(`(?i)\bkubectl\s+delete\b`), "kubectl delete removes cluster resources"},
	{regexp.MustCompile(`(?i)\b(dotnet\s+ef\s+database\s+drop|dropdb)\b`), "drops a database"},
	{regexp.MustCompile(`(?i)\bshred\b|\bwipefs\b`), "secure wipe"},
	{regexp.MustCompile(`:\(\)\s*\{\s*:\|:&\s*\}\s*;?\s*:`), "fork bomb"},
	{regexp.MustCompile(`(?i)\bchmod\s+(-r\s+)?000\b`), "removes all permissions"},
	{regexp.MustCompile(`(?i)--no-preserve-root`), "rm on root"},
	{regexp.MustCompile(`(?i)\bflushall\b|\bflushdb\b`), "flushes a datastore"},
}

// redirRe: single > redirection (not >>, not 2>, not >&) — may overwrite an
// existing file. We can't know the target exists without racing, so flag it.
var redirRe = regexp.MustCompile(`[^>2&]>\s*[^>&\s]|^>\s*\S`)

// Check inspects a fully-substituted command line. modelHint is the
// destructive flag reported by the model.
func Check(command string, modelHint bool) Verdict {
	c := strings.TrimSpace(command)
	for _, r := range rules {
		if r.re.MatchString(c) {
			return Verdict{Destructive: true, Reason: r.reason}
		}
	}
	if redirRe.MatchString(c) {
		return Verdict{Destructive: true, Reason: "output redirection may overwrite a file"}
	}
	if modelHint {
		return Verdict{Destructive: true, Reason: "model flagged this as destructive"}
	}
	return Verdict{}
}

// shellStateRe: commands that only make sense inside the current shell.
var shellStateRe = regexp.MustCompile(`(?i)^(cd|pushd|popd|export|unset|set\s|setx|source|\.\s|alias|activate\b|conda\s+activate|\$env:)`)

// ChangesShellState reports whether a command mutates shell state and must
// be eval'd by the shell (or run manually) rather than in a subprocess.
//
// The deterministic check is authoritative in both directions for anything
// it can classify: small local models set shell_state unreliably (observed:
// `git push` flagged true), and a false positive silently stops execution.
// The model hint is only honored when the first word is not a known
// executable, i.e. when we have no local evidence of our own. knownExe may
// be nil.
func ChangesShellState(command string, modelHint bool, knownExe func(string) bool) bool {
	c := strings.TrimSpace(command)
	if shellStateRe.MatchString(c) {
		return true
	}
	// "<venv>\Scripts\Activate.ps1" or "source venv/bin/activate"
	if strings.Contains(strings.ToLower(c), "activate") && !strings.Contains(c, "deactivate") {
		return true
	}
	if fields := strings.Fields(c); len(fields) > 0 && knownExe != nil && knownExe(fields[0]) {
		return false
	}
	return modelHint
}
