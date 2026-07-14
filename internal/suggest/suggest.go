// Package suggest defines the suggestion type shared by both tiers and the
// hard validation applied to anything the model returns. Nothing is ever
// executed unless it passed through Validate.
package suggest

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// Source identifies which tier produced a suggestion.
type Source int

const (
	SourceTier1 Source = iota + 1
	SourceTier2
)

func (s Source) String() string {
	switch s {
	case SourceTier1:
		return "tier1 (derived typo fixer)"
	case SourceTier2:
		return "tier2 (local LLM)"
	}
	return "unknown"
}

// Suggestion is a single proposed command line.
type Suggestion struct {
	Command      string   `json:"command"`
	Explanation  string   `json:"explanation"`
	Confidence   float64  `json:"confidence"`
	Placeholders []string `json:"placeholders"`
	Destructive  bool     `json:"destructive"`
	ShellState   bool     `json:"shell_state"` // changes shell state (cd, export, venv activate, ...)

	Source Source `json:"-"`
}

var (
	ErrInvalidJSON = errors.New("model returned invalid JSON")
	ErrRejected    = errors.New("model output failed validation")
)

// fenceRe strips a leading/trailing markdown code fence if the model wrapped
// its JSON in one despite instructions.
var fenceRe = regexp.MustCompile("(?s)^\\s*```[a-zA-Z]*\\s*(.*?)\\s*```\\s*$")

// Parse decodes and validates raw model output into a Suggestion.
func Parse(raw string) (*Suggestion, error) {
	raw = strings.TrimSpace(raw)
	if m := fenceRe.FindStringSubmatch(raw); m != nil {
		raw = m[1]
	}
	// Tolerate leading/trailing prose by extracting the outermost JSON object.
	if i := strings.Index(raw, "{"); i > 0 {
		raw = raw[i:]
	}
	if i := strings.LastIndex(raw, "}"); i >= 0 && i < len(raw)-1 {
		raw = raw[:i+1]
	}

	var s Suggestion
	dec := json.NewDecoder(strings.NewReader(raw))
	if err := dec.Decode(&s); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidJSON, err)
	}
	if err := s.validate(); err != nil {
		return nil, err
	}
	s.Source = SourceTier2
	return &s, nil
}

// placeholderRe matches {name} tokens inside a command.
var placeholderRe = regexp.MustCompile(`\{([A-Za-z][A-Za-z0-9_-]*)\}`)

func (s *Suggestion) validate() error {
	s.Command = sanitizeCommand(s.Command)
	if s.Confidence < 0 || s.Confidence > 1 {
		return fmt.Errorf("%w: confidence %v out of range", ErrRejected, s.Confidence)
	}
	if s.Command == "" {
		// A deliberate "I can't tell" from the model — valid, the caller
		// reports "no suggestion".
		s.Confidence = 0
		s.Placeholders = nil
		return nil
	}
	if strings.ContainsAny(s.Command, "\n\r") {
		return fmt.Errorf("%w: command is not a single line", ErrRejected)
	}
	if len(s.Command) > 500 {
		return fmt.Errorf("%w: command too long", ErrRejected)
	}
	// Placeholders listed must appear in the command; placeholders present in
	// the command but not listed are added so the user is always prompted.
	found := map[string]bool{}
	for _, m := range placeholderRe.FindAllStringSubmatch(s.Command, -1) {
		found[m[1]] = true
	}
	var ph []string
	seen := map[string]bool{}
	for _, p := range s.Placeholders {
		p = strings.Trim(strings.TrimSpace(p), "{}<>")
		if p == "" || seen[p] {
			continue
		}
		if !found[p] {
			continue // listed but not in command: drop silently
		}
		seen[p] = true
		ph = append(ph, p)
	}
	for name := range found {
		if !seen[name] {
			ph = append(ph, name)
			seen[name] = true
		}
	}
	s.Placeholders = ph
	s.Explanation = strings.TrimSpace(strings.ReplaceAll(s.Explanation, "\n", " "))
	return nil
}

// sanitizeCommand strips markdown, backticks, shell prompts and surrounding
// quotes the model may have added, and collapses to a single trimmed line.
func sanitizeCommand(c string) string {
	c = strings.TrimSpace(c)
	if m := fenceRe.FindStringSubmatch(c); m != nil {
		c = strings.TrimSpace(m[1])
	}
	c = strings.Trim(c, "`")
	c = strings.TrimSpace(c)
	for _, p := range []string{"$ ", "> ", "PS> "} {
		c = strings.TrimPrefix(c, p)
	}
	// Take only the first non-empty line — a command line, not a script.
	for _, line := range strings.Split(c, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			return line
		}
	}
	return ""
}

// Display returns the command with {name} placeholders rendered as <name>
// for human eyes.
func (s *Suggestion) Display() string {
	return placeholderRe.ReplaceAllString(s.Command, "<$1>")
}

// Fill substitutes placeholder values into the command.
func (s *Suggestion) Fill(values map[string]string) string {
	return placeholderRe.ReplaceAllStringFunc(s.Command, func(m string) string {
		name := placeholderRe.FindStringSubmatch(m)[1]
		if v, ok := values[name]; ok && v != "" {
			return v
		}
		return m
	})
}
