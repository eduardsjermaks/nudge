// Package prompt builds the compact Tier-2 prompt. Exactly and only this is
// sent to the local model: OS + shell name, project marker file *names* from
// the cwd (never contents), the user's input, and in fix mode the exit code.
package prompt

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

//go:embed prompt.txt
var System string

// markerGlobs are the project files whose *names* give the model context.
var markerGlobs = []string{
	"*.csproj", "*.sln", "*.fsproj",
	"package.json", "pnpm-lock.yaml", "yarn.lock", "deno.json",
	"go.mod", "Cargo.toml",
	"pom.xml", "build.gradle", "build.gradle.kts", "settings.gradle",
	"pyproject.toml", "requirements.txt", "setup.py", "Pipfile",
	"Gemfile", "composer.json", "mix.exs", "CMakeLists.txt", "Makefile",
	"Dockerfile", "docker-compose.yml", "compose.yaml",
	".terraform.lock.hcl", "Chart.yaml",
}

// ProjectMarkers returns marker file names found in dir (cwd), capped so the
// prompt stays compact.
func ProjectMarkers(dir string) []string {
	var out []string
	seen := map[string]bool{}
	for _, g := range markerGlobs {
		matches, _ := filepath.Glob(filepath.Join(dir, g))
		for _, m := range matches {
			name := filepath.Base(m)
			if !seen[name] {
				seen[name] = true
				out = append(out, name)
			}
		}
	}
	sort.Strings(out)
	if len(out) > 12 {
		out = out[:12]
	}
	return out
}

// ShellName guesses the invoking shell from the environment; best-effort.
func ShellName() string {
	if runtime.GOOS == "windows" {
		if os.Getenv("PSModulePath") != "" {
			return "powershell"
		}
		return "cmd"
	}
	if sh := os.Getenv("SHELL"); sh != "" {
		return filepath.Base(sh)
	}
	return "sh"
}

// Request describes one correction request.
type Request struct {
	Input    string // the failed command line, or the plain-words intent
	FixMode  bool   // true = a real command failed; false = intent
	ExitCode int    // exit code in fix mode, if known (0 = unknown/not run)
	Dir      string // cwd for project markers
	Shell    string // override; empty = autodetect
}

// User renders the user message for the model.
func (r Request) User() string {
	var b strings.Builder
	if r.FixMode {
		if r.ExitCode > 0 {
			fmt.Fprintf(&b, "Input: command `%s` failed (exit %d).", r.Input, r.ExitCode)
		} else {
			fmt.Fprintf(&b, "Input: command `%s` failed.", r.Input)
		}
	} else {
		fmt.Fprintf(&b, "Input: the user wants: %s.", r.Input)
	}
	shell := r.Shell
	if shell == "" {
		shell = ShellName()
	}
	fmt.Fprintf(&b, " OS: %s, shell: %s.", runtime.GOOS, shell)
	if markers := ProjectMarkers(r.Dir); len(markers) > 0 {
		fmt.Fprintf(&b, " Project files: %s.", strings.Join(markers, ", "))
	}
	return b.String()
}
