// Package tier1 is the derived typo fixer: no LLM, no configuration. It
// harvests executable names from PATH and subcommand names from the tools
// themselves, then applies Damerau-Levenshtein with a strict confidence bar.
// Pure keyboard slips are fixed instantly; anything fuzzier falls through to
// Tier 2.
package tier1

import (
	"sort"
	"strings"

	"nudge/internal/suggest"
)

// Match tries to fix the given argv as a pure typo. Returns nil when Tier 1
// is not confident — the caller then falls through to the LLM tier.
func Match(words []string) *suggest.Suggestion {
	if len(words) == 0 {
		return nil
	}
	exes := Executables()
	if len(exes) == 0 {
		return nil
	}

	fixed := make([]string, len(words))
	copy(fixed, words)
	changed := false

	first := strings.ToLower(words[0])
	tool := first
	if !contains(exes, first) {
		m := firstWordMatch(first, exes)
		if m == "" {
			return nil // first word isn't a confident near-miss of anything real
		}
		fixed[0] = m
		tool = m
		changed = true
	}

	// If the (corrected) tool is a known multi-command CLI, check its
	// subcommand too — catches `git pshu` and `gti pshu` alike.
	if len(words) >= 2 && IsMultiCmdTool(tool) && !strings.HasPrefix(words[1], "-") {
		sub := strings.ToLower(words[1])
		if subs := Subcommands(tool); len(subs) > 0 && !contains(subs, sub) {
			m := bestMatch(sub, subs)
			if m == "" {
				// Real tool, but the subcommand is not a pure typo of
				// anything it offers — that's the LLM's job, even if we
				// fixed the tool name (too risky to guess half a fix).
				return nil
			}
			fixed[1] = m
			changed = true
		}
	}

	if !changed {
		return nil // everything already valid — not a typo problem
	}

	return &suggest.Suggestion{
		Command:     joinArgs(fixed),
		Explanation: "typo fix for `" + joinArgs(words) + "`",
		Confidence:  0.97,
		Source:      suggest.SourceTier1,
	}
}

// firstWordMatch corrects the executable name itself. Short words are held
// to a stricter bar than the generic one, because 3-letter near-misses are
// everywhere on PATH (`new` -> `net`) and a wrong Tier-1 fix is worse than
// falling through to the model:
//   - length < 3: never corrected
//   - length 3:   only adjacent transpositions (gti -> git), i.e. the same
//     letters in swapped order
//   - length >= 4: the generic bar (bestMatch)
func firstWordMatch(word string, exes []string) string {
	m := bestMatch(word, exes)
	if m == "" {
		return ""
	}
	if len([]rune(word)) == 3 && !sameRunes(word, m) {
		return ""
	}
	return m
}

func sameRunes(a, b string) bool {
	ra, rb := []rune(a), []rune(b)
	if len(ra) != len(rb) {
		return false
	}
	sort.Slice(ra, func(i, j int) bool { return ra[i] < ra[j] })
	sort.Slice(rb, func(i, j int) bool { return rb[i] < rb[j] })
	return string(ra) == string(rb)
}

func contains(sorted []string, w string) bool {
	i := sort.SearchStrings(sorted, w)
	return i < len(sorted) && sorted[i] == w
}

// joinArgs reassembles argv into a display/exec string, quoting args that
// contain whitespace.
func joinArgs(words []string) string {
	parts := make([]string, len(words))
	for i, w := range words {
		if strings.ContainsAny(w, " \t") {
			parts[i] = `"` + w + `"`
		} else {
			parts[i] = w
		}
	}
	return strings.Join(parts, " ")
}
