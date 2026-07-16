//go:build windows

package setup

import "syscall"

const (
	createNewProcessGroup = 0x00000200
	detachedProcess       = 0x00000008
)

// detachAttrs keeps `ollama serve` alive after nudge exits and detached from
// this console, so closing the terminal cannot kill the server.
func detachAttrs() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{CreationFlags: createNewProcessGroup | detachedProcess}
}
