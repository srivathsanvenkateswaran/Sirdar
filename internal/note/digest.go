package note

import (
	"bytes"
	"fmt"
	"strings"
	"text/tabwriter"
)

// DigestRow is one run's summary line in a Digest.
type DigestRow struct {
	Key, Priority, Issue, Confidence, Classification, State, RunID, Reason string
}

const issueMaxLen = 60

// blockedReasonStates lists the states a Digest explains with a reason line
// below the table.
var blockedReasonStates = map[string]bool{
	"blocked":     true,
	"failed":      true,
	"over_budget": true,
}

// Digest renders rows as an aligned text table with columns KEY, PRIO,
// ISSUE, CONF, CLASS, STATE, RUN (Issue truncated to 60 characters),
// followed by one "<KEY>: <state> — <reason>" line for every row whose
// State is blocked, failed, or over_budget.
func Digest(rows []DigestRow) string {
	var buf bytes.Buffer
	w := tabwriter.NewWriter(&buf, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "KEY\tPRIO\tISSUE\tCONF\tCLASS\tSTATE\tRUN")
	for _, r := range rows {
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
			r.Key, r.Priority, truncateIssue(r.Issue), r.Confidence, r.Classification, r.State, r.RunID)
	}
	w.Flush()

	out := buf.String()

	var reasons []string
	for _, r := range rows {
		if blockedReasonStates[r.State] {
			reasons = append(reasons, fmt.Sprintf("%s: %s — %s", r.Key, r.State, r.Reason))
		}
	}
	if len(reasons) > 0 {
		out = strings.TrimRight(out, "\n") + "\n\n" + strings.Join(reasons, "\n") + "\n"
	}
	return out
}

func truncateIssue(s string) string {
	if len(s) <= issueMaxLen {
		return s
	}
	return s[:issueMaxLen]
}
