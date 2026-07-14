// Package ui implements the terminal interaction: colors (hand-rolled ANSI,
// NO_COLOR honored), a minimal stderr spinner, and the confirmation prompt.
package ui

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

var (
	stdinReader     = bufio.NewReader(os.Stdin)
	colorEnabled    bool
	colorEnabledSet bool
)

// IsTTY reports whether f is attached to a terminal.
func IsTTY(f *os.File) bool {
	info, err := f.Stat()
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeCharDevice != 0
}

// ColorEnabled: colors only when stderr is a TTY, NO_COLOR is unset, and the
// console understands ANSI (on Windows we try to switch it on).
func ColorEnabled() bool {
	if colorEnabledSet {
		return colorEnabled
	}
	colorEnabledSet = true
	colorEnabled = os.Getenv("NO_COLOR") == "" && IsTTY(os.Stderr) && enableVT()
	return colorEnabled
}

func paint(code, s string) string {
	if !ColorEnabled() {
		return s
	}
	return "\x1b[" + code + "m" + s + "\x1b[0m"
}

func Bold(s string) string   { return paint("1", s) }
func Dim(s string) string    { return paint("2", s) }
func Cyan(s string) string   { return paint("36", s) }
func Yellow(s string) string { return paint("33", s) }
func Red(s string) string    { return paint("31", s) }

// Errf writes formatted text to stderr (all nudge UI lives on stderr so
// stdout stays clean for shell-eval mode).
func Errf(format string, a ...any) {
	fmt.Fprintf(os.Stderr, format, a...)
}

// --- Spinner ---

type Spinner struct {
	stop chan struct{}
	done sync.WaitGroup
	on   bool
}

// StartSpinner shows a minimal spinner on stderr, TTY only.
func StartSpinner(label string) *Spinner {
	s := &Spinner{stop: make(chan struct{})}
	if !IsTTY(os.Stderr) {
		return s
	}
	s.on = true
	s.done.Add(1)
	go func() {
		defer s.done.Done()
		frames := `-\|/`
		i := 0
		t := time.NewTicker(120 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-s.stop:
				fmt.Fprintf(os.Stderr, "\r%s\r", strings.Repeat(" ", len(label)+2))
				return
			case <-t.C:
				fmt.Fprintf(os.Stderr, "\r%c %s", frames[i%len(frames)], label)
				i++
			}
		}
	}()
	return s
}

func (s *Spinner) Stop() {
	if !s.on {
		return
	}
	s.on = false
	close(s.stop)
	s.done.Wait()
}

// --- Prompts ---

// Decision is the user's answer at the confirmation prompt.
type Decision int

const (
	Run Decision = iota
	No
	Edit
)

// Confirm asks the user whether to run the command. When strict is true
// (destructive or low-confidence suggestions), plain Enter means NO and an
// explicit typed "y" is required.
func Confirm(strict bool) (Decision, error) {
	if strict {
		Errf("Run it? [y = yes / Enter or n = no / e = edit] ")
	} else {
		Errf("Run it? [Enter = yes / n = no / e = edit] ")
	}
	line, err := readLine()
	if err != nil {
		return No, err
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "y", "yes":
		return Run, nil
	case "":
		if strict {
			return No, nil
		}
		return Run, nil
	case "e", "edit":
		return Edit, nil
	default:
		return No, nil
	}
}

// EditLine lets the user amend the command. True inline prefill needs raw
// terminal mode (a heavy dep), so we print the command and read a
// replacement; Enter keeps it unchanged.
func EditLine(current string) (string, error) {
	Errf("  current: %s\n", Bold(current))
	Errf("  new command (Enter = keep as is): ")
	line, err := readLine()
	if err != nil {
		return "", err
	}
	line = strings.TrimSpace(line)
	if line == "" {
		return current, nil
	}
	return line, nil
}

// AskValue prompts for a placeholder value.
func AskValue(name string) (string, error) {
	for {
		Errf("  value for %s: ", Cyan("<"+name+">"))
		line, err := readLine()
		if err != nil {
			return "", err
		}
		line = strings.TrimSpace(line)
		if line != "" {
			return line, nil
		}
	}
}

func readLine() (string, error) {
	line, err := stdinReader.ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	// PowerShell 5.1 prepends a UTF-8 BOM to piped input, and some configs
	// pipe UTF-16 (interleaved NULs) - strip both so scripted input works.
	line = strings.ReplaceAll(line, "\x00", "")
	line = strings.ReplaceAll(line, string(rune(0xFEFF)), "")
	return strings.TrimRight(line, "\r\n"), nil
}
