package shell

import (
	"strings"
	"testing"
)

func TestInitSnippets(t *testing.T) {
	for _, sh := range []string{"bash", "zsh", "fish", "powershell", "pwsh"} {
		s, err := Init(sh)
		if err != nil {
			t.Fatalf("Init(%s): %v", sh, err)
		}
		if !strings.Contains(s, "NUDGE_HISTORY") {
			t.Errorf("%s snippet must pass history via NUDGE_HISTORY", sh)
		}
		if !strings.Contains(s, "--shell-eval") {
			t.Errorf("%s snippet must call the binary in shell-eval mode", sh)
		}
		if !strings.Contains(strings.ToLower(s), "fix") {
			t.Errorf("%s snippet must install the fix alias", sh)
		}
		// Explicit `nudge <words>` must go through shell-eval too, so
		// shell-state suggestions (cd, activate) take effect — but the
		// subcommands must bypass it or their stdout would be eval'd.
		for _, sub := range []string{"init", "doctor", "setup"} {
			if !strings.Contains(s, sub) {
				t.Errorf("%s snippet must pass the %s subcommand through directly", sh, sub)
			}
		}
		if strings.Count(s, "--shell-eval") < 2 {
			t.Errorf("%s snippet must use shell-eval for both bare and explicit invocations", sh)
		}
	}
	// each shell gets its own not-found hook name
	for sh, hook := range map[string]string{
		"bash":       "command_not_found_handle",
		"zsh":        "command_not_found_handler",
		"fish":       "fish_command_not_found",
		"powershell": "CommandNotFoundAction",
	} {
		s, _ := Init(sh)
		if !strings.Contains(s, hook) {
			t.Errorf("%s snippet missing %s hook", sh, hook)
		}
	}
	if _, err := Init("tcsh"); err == nil {
		t.Error("unknown shell should error")
	}
}

func TestPickLastCommand(t *testing.T) {
	cases := []struct {
		hist string
		want string
	}{
		{"git status\ndotnet create migrations\n nudge", "dotnet create migrations"},
		{"git status\ndotnet create migrations\nfix", "dotnet create migrations"},
		{"dotnet create migrations", "dotnet create migrations"},
		{"nudge\nfix\n", ""},
		{"", ""},
		{"  git pshu  \nnudge.exe", "git pshu"},
	}
	for _, c := range cases {
		if got := PickLastCommand(c.hist); got != c.want {
			t.Errorf("PickLastCommand(%q) = %q, want %q", c.hist, got, c.want)
		}
	}
}
