package tier1

import "testing"

func TestDistance(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"push", "push", 0},
		{"pshu", "push", 2}, // rotation, not one swap; bestMatch treats anagrams as dist 1
		{"gti", "git", 1},
		{"comit", "commit", 1},
		{"statsu", "status", 1},
		{"puhs", "push", 1},
		{"create", "clean", 3},
		{"remove", "rm", 4},
		{"abc", "xyz", 3},
	}
	for _, c := range cases {
		if got := Distance(c.a, c.b); got != c.want {
			t.Errorf("Distance(%q,%q) = %d, want %d", c.a, c.b, got, c.want)
		}
	}
}

func TestBestMatch(t *testing.T) {
	subs := []string{"add", "branch", "checkout", "clean", "clone", "commit", "pull", "push", "rebase", "status"}

	if got := bestMatch("pshu", subs); got != "push" {
		t.Errorf("pshu -> %q, want push", got)
	}
	if got := bestMatch("comit", subs); got != "commit" {
		t.Errorf("comit -> %q, want commit", got)
	}
	if got := bestMatch("statsu", subs); got != "status" {
		t.Errorf("statsu -> %q, want status", got)
	}
	// already valid: no correction
	if got := bestMatch("push", subs); got != "" {
		t.Errorf("push -> %q, want no match", got)
	}
	// nowhere close: fall through to tier 2
	if got := bestMatch("create", subs); got != "" {
		t.Errorf("create -> %q, want no match (LLM's job)", got)
	}
	if got := bestMatch("qwerty", subs); got != "" {
		t.Errorf("qwerty -> %q, want no match", got)
	}
	// ambiguous tie must not fire
	if got := bestMatch("pul", []string{"pull", "pun", "put"}); got != "" {
		t.Errorf("ambiguous pul -> %q, want no match", got)
	}
	// too short to guess
	if got := bestMatch("gt", []string{"git", "gh"}); got != "" {
		t.Errorf("gt -> %q, want no match", got)
	}
}

func TestFirstWordMatch(t *testing.T) {
	exes := []string{"docker", "git", "net", "node", "npm", "python"}
	// 3-letter words: only transpositions are trusted
	if got := firstWordMatch("gti", exes); got != "git" {
		t.Errorf("gti -> %q, want git", got)
	}
	if got := firstWordMatch("new", exes); got != "" {
		t.Errorf("new -> %q, want no match (substitution on 3 letters is too risky)", got)
	}
	// longer words: normal bar
	if got := firstWordMatch("pyhton", exes); got != "python" {
		t.Errorf("pyhton -> %q, want python", got)
	}
	if got := firstWordMatch("dcoker", exes); got != "docker" {
		t.Errorf("dcoker -> %q, want docker", got)
	}
}

func TestMatchIntentPhrasesFallThrough(t *testing.T) {
	// Plain-words intent must never be "typo-fixed" by tier 1.
	for _, words := range [][]string{
		{"undo", "last", "commit"},
		{"new", "migration", "AddOrders"},
		{"asdfgh", "qwerty"},
	} {
		if s := Match(words); s != nil {
			t.Errorf("Match(%v) = %q, want nil (fall through to tier 2)", words, s.Command)
		}
	}
}

func TestHarvestHelpParsing(t *testing.T) {
	// The generic heuristic, exercised on canned help-ish output shapes.
	out := `Usage: tool <command>

Commands:
  build       Build the project
  clean       Remove build outputs
  push        Upload artifacts
  -h, --help  Show help
`
	subs := parseHelpText(out)
	want := map[string]bool{"build": true, "clean": true, "push": true}
	for w := range want {
		if !containsStr(subs, w) {
			t.Errorf("expected %q in %v", w, subs)
		}
	}
	if containsStr(subs, "-h") {
		t.Errorf("flags must not be harvested: %v", subs)
	}

	// git help -a columned style
	out2 := `See 'git help <command>' to read about a specific subcommand

Main Porcelain Commands
   add                     clone                   push
   branch                  commit                  status
`
	subs2 := parseHelpText(out2)
	for _, w := range []string{"add", "clone", "push", "branch", "commit", "status"} {
		if !containsStr(subs2, w) {
			t.Errorf("expected %q in %v", w, subs2)
		}
	}
}

func containsStr(list []string, w string) bool {
	for _, s := range list {
		if s == w {
			return true
		}
	}
	return false
}
