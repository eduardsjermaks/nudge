// Package execx runs a confirmed command line through the user's shell and
// propagates its exit code.
package execx

import (
	"errors"
	"os"
	"os/exec"
	"runtime"
)

// Run executes the command line via the platform shell, wiring through
// stdio. Returns the child's exit code.
func Run(command string) int {
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		shell := "powershell.exe"
		if _, err := exec.LookPath("pwsh.exe"); err == nil {
			shell = "pwsh.exe"
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
