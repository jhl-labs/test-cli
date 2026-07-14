package runner

import (
	"strconv"
	"strings"
)

// shellJoin renders an argv slice for display, quoting args with spaces.
func shellJoin(args []string) string {
	parts := make([]string, len(args))
	for i, a := range args {
		if strings.ContainsAny(a, " \t\r\n\"'") {
			parts[i] = strconv.Quote(a)
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
	start := len(out) - max(0, n)
	for start < len(out) && out[start]&0xc0 == 0x80 {
		start++
	}
	tail := out[start:]
	return append([]byte("…(truncated)…\n"), tail...)
}
