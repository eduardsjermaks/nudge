// Package execx runs a confirmed command line through the user's shell and
// propagates its exit code.
package execx

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// Run executes the command line via the platform shell, wiring through
// stdio. Returns the child's exit code.
func Run(command string) int {
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		shell := "powershell.exe"
		if _, err := exec.LookPath("pwsh.exe"); err == nil {
			shell = "pwsh.exe"
		} else {
			// Windows PowerShell 5.1 predates the && operator; models
			// suggest it anyway. Rewrite so the chain still works.
			command = RewriteAndChains(command)
		}
		// Without the trailing exit, powershell -Command collapses any
		// nonzero native exit code to 1, breaking propagation.
		cmd = exec.Command(shell, "-NoProfile", "-Command", command+"; exit $LASTEXITCODE")
	} else {
		shell := os.Getenv("SHELL")
		if shell == "" {
			shell = "/bin/sh"
		}
		cmd = exec.Command(shell, "-c", command)
	}
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	err := cmd.Run()
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	return 1
}

// RewriteAndChains turns `a && b && c` into `a; if ($?) { b; if ($?) { c } }`
// — `&&` is a parse error in Windows PowerShell 5.1 and the guarded form
// behaves identically on 5.1 and 7, so callers targeting any PowerShell can
// apply it unconditionally (shell-eval output does). Conservative: a command
// containing quotes is returned unchanged (a quoted "&&" must not be
// rewritten), which then fails in 5.1 exactly as it would have anyway.
func RewriteAndChains(command string) string {
	if !strings.Contains(command, "&&") || strings.ContainsAny(command, `"'`) {
		return command
	}
	parts := strings.Split(command, "&&")
	out := strings.TrimSpace(parts[len(parts)-1])
	for i := len(parts) - 2; i >= 0; i-- {
		out = fmt.Sprintf("%s; if ($?) { %s }", strings.TrimSpace(parts[i]), out)
	}
	return out
}
