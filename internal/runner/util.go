package runner

import "strings"

// shellJoin renders an argv slice for display, quoting args with spaces.
func shellJoin(args []string) string {
	parts := make([]string, len(args))
	for i, a := range args {
		if strings.ContainsAny(a, " \t\"'") {
			parts[i] = "\"" + strings.ReplaceAll(a, "\"", "\\\"") + "\""
		} else {
			parts[i] = a
		}
	}
	return strings.Join(parts, " ")
}

// trimTail returns the last n bytes of out, prefixed with an ellipsis marker
// when truncated, so CI logs surface the most relevant framework output.
func trimTail(out []byte, n int) []byte {
	if len(out) <= n {
		return out
	}
	tail := out[len(out)-n:]
	return append([]byte("…(truncated)…\n"), tail...)
}
