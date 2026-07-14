//go:build windows

package ui

import (
	"os"
	"syscall"
	"unsafe"
)

// enableVT switches the Windows console to VT (ANSI) processing so our
// hand-rolled escape codes render. Windows Terminal has it on already;
// legacy conhost needs SetConsoleMode. Returns false if it can't be enabled,
// in which case we simply print without color.
func enableVT() bool {
	const enableVirtualTerminalProcessing = 0x0004
	k32 := syscall.NewLazyDLL("kernel32.dll")
	getMode := k32.NewProc("GetConsoleMode")
	setMode := k32.NewProc("SetConsoleMode")

	h := syscall.Handle(os.Stderr.Fd())
	var mode uint32
	if r, _, _ := getMode.Call(uintptr(h), uintptr(unsafe.Pointer(&mode))); r == 0 {
		return false
	}
	if mode&enableVirtualTerminalProcessing != 0 {
		return true
	}
	r, _, _ := setMode.Call(uintptr(h), uintptr(mode|enableVirtualTerminalProcessing))
	return r != 0
}
