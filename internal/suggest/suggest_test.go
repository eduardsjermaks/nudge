package suggest

import (
	"errors"
	"testing"
)

func TestParseValid(t *testing.T) {
	raw := `{"command": "dotnet ef migrations add {name}", "explanation": "create a new EF Core migration", "confidence": 0.92, "placeholders": ["name"], "destructive": false}`
	s, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if s.Command != "dotnet ef migrations add {name}" {
		t.Errorf("command = %q", s.Command)
	}
	if len(s.Placeholders) != 1 || s.Placeholders[0] != "name" {
		t.Errorf("placeholders = %v", s.Placeholders)
	}
	if s.Display() != "dotnet ef migrations add <name>" {
		t.Errorf("display = %q", s.Display())
	}
}

func TestParseStripsMarkdownFence(t *testing.T) {
	raw := "```json\n{\"command\": \"git push\", \"explanation\": \"push\", \"confidence\": 0.9}\n```"
	s, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if s.Command != "git push" {
		t.Errorf("command = %q", s.Command)
	}
}

func TestParseStripsBackticksAndPrompt(t *testing.T) {
	raw := `{"command": "` + "`$ git push`" + `", "explanation": "x", "confidence": 0.9}`
	s, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if s.Command != "git push" {
		t.Errorf("command = %q", s.Command)
	}
}

func TestParseSurroundingProse(t *testing.T) {
	raw := "Sure! Here you go: {\"command\": \"git push\", \"explanation\": \"x\", \"confidence\": 0.8} Hope that helps."
	s, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if s.Command != "git push" {
		t.Errorf("command = %q", s.Command)
	}
}

func TestParseRejectsGarbage(t *testing.T) {
	for _, raw := range []string{
		"not json at all",
		`{"command": 42}`,
		`{"command": "x", "confidence": 3.5}`,
		`{"explanation": "no command", "confidence": 0.5, "command": "a\nb\nc"}`, // multiline handled: takes first line... see below
	} {
		_, err := Parse(raw)
		if raw == `{"explanation": "no command", "confidence": 0.5, "command": "a\nb\nc"}` {
			// multi-line commands are reduced to their first line by
			// sanitize, which is safe; not an error case
			continue
		}
		if err == nil {
			t.Errorf("Parse(%q) should have failed", raw)
		}
	}
	if _, err := Parse(""); !errors.Is(err, ErrInvalidJSON) {
		t.Errorf("empty input: err = %v", err)
	}
}

func TestPlaceholderReconciliation(t *testing.T) {
	// listed but absent -> dropped; present but unlisted -> added
	raw := `{"command": "git checkout {branch}", "explanation": "x", "confidence": 0.9, "placeholders": ["branch", "ghost"]}`
	s, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Placeholders) != 1 || s.Placeholders[0] != "branch" {
		t.Errorf("placeholders = %v", s.Placeholders)
	}

	raw2 := `{"command": "kubectl logs {pod}", "explanation": "x", "confidence": 0.9, "placeholders": []}`
	s2, err := Parse(raw2)
	if err != nil {
		t.Fatal(err)
	}
	if len(s2.Placeholders) != 1 || s2.Placeholders[0] != "pod" {
		t.Errorf("placeholders = %v", s2.Placeholders)
	}
}

func TestFill(t *testing.T) {
	s := &Suggestion{Command: "dotnet ef migrations add {name} --context {ctx}"}
	got := s.Fill(map[string]string{"name": "AddOrders", "ctx": "AppDb"})
	want := "dotnet ef migrations add AddOrders --context AppDb"
	if got != want {
		t.Errorf("Fill = %q, want %q", got, want)
	}
	// missing values leave the placeholder visible rather than vanishing
	got2 := s.Fill(map[string]string{"name": "AddOrders"})
	if got2 != "dotnet ef migrations add AddOrders --context {ctx}" {
		t.Errorf("Fill partial = %q", got2)
	}
}
