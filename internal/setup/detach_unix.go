//go:build !windows

package setup

import "syscall"

// detachAttrs keeps `ollama serve` alive after nudge exits and out of the
// terminal's process group, so Ctrl+C at the prompt cannot kill the server.
func detachAttrs() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setsid: true}
}
