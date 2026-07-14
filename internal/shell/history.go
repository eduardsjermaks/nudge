package shell

import "strings"

// PickLastCommand selects the most recent real command from the NUDGE_HISTORY
// env var set by the wrapper function. Some shells (bash) already include the
// in-flight `nudge`/`fix` invocation in history, so those lines are skipped.
func PickLastCommand(historyEnv string) string {
	lines := strings.Split(historyEnv, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			continue
		}
		first := strings.ToLower(strings.Fields(line)[0])
		first = strings.TrimSuffix(first, ".exe")
		if first == "nudge" || first == "fix" {
			continue
		}
		return line
	}
	return ""
}
