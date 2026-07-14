//go:build !windows

package ui

// enableVT: on POSIX terminals ANSI is a given when stderr is a TTY.
func enableVT() bool { return true }
