package service

import (
	"regexp"
	"strconv"
	"strings"
)

// Workflow constants and the pure parsers behind the derived workflow fields (phase 4). The
// label names are hard-coded on purpose (a project that does not use them cannot record gate
// decisions); nothing here touches the database.

// Gate names a human decision point that can be recorded in the app (gate 3, the merge, is a
// fact on GitHub and has no in-app decision).
type Gate string

const (
	GateRequirements Gate = "requirements"
	GatePlan         Gate = "plan"
)

// Gate labels (agentic workflow 5.1).
const (
	LabelAwaitingReqApproval  = "awaiting-req-approval"
	LabelAwaitingPlanApproval = "awaiting-plan-approval"
	LabelAwaitingMerge        = "awaiting-merge"
)

// Verdict is the outcome of a gate decision.
type Verdict string

const (
	VerdictApproved         Verdict = "approved"
	VerdictChangesRequested Verdict = "changes_requested"
)

// Valid reports whether g is a decision gate.
func (g Gate) Valid() bool { _, ok := GateLabel(g); return ok }

// Valid reports whether v is a verdict.
func (v Verdict) Valid() bool { return v == VerdictApproved || v == VerdictChangesRequested }

// GateLabel is the awaiting-* label a ticket must carry for a decision on g.
func GateLabel(g Gate) (string, bool) {
	switch g {
	case GateRequirements:
		return LabelAwaitingReqApproval, true
	case GatePlan:
		return LabelAwaitingPlanApproval, true
	}
	return "", false
}

const (
	linkScanLines = 50
	maxPRURLLen   = 500
	maxBranchLen  = 200
	maxRoundCap   = 99
)

var linkLine = regexp.MustCompile(`^(PR|Branch):\s*(\S+)\s*$`)

// ParseDescriptionLinks derives the PR url and branch from `PR: <url>` and `Branch: <name>`
// lines in the first 50 lines of a description. The key is case-sensitive and must start the
// line, the first occurrence of a key wins (even if its value is then rejected), CRLF is
// tolerated, the url must be https and at most 500 characters, the branch at most 200.
func ParseDescriptionLinks(desc string) (prURL, branch *string) {
	seenPR, seenBranch := false, false
	lines := strings.Split(strings.ReplaceAll(desc, "\r\n", "\n"), "\n")
	if len(lines) > linkScanLines {
		lines = lines[:linkScanLines]
	}
	for _, line := range lines {
		m := linkLine.FindStringSubmatch(strings.TrimRight(line, "\r"))
		if m == nil {
			continue
		}
		switch m[1] {
		case "PR":
			if seenPR {
				continue
			}
			seenPR = true
			if strings.HasPrefix(m[2], "https://") && len(m[2]) > len("https://") && len(m[2]) <= maxPRURLLen {
				v := m[2]
				prURL = &v
			}
		case "Branch":
			if seenBranch {
				continue
			}
			seenBranch = true
			if len(m[2]) <= maxBranchLen {
				v := m[2]
				branch = &v
			}
		}
	}
	return prURL, branch
}

var (
	roundTagged = regexp.MustCompile(`^\[orchestrator\]\s+ROUND\s+(\d+)/(\d+)\b`)
	roundLine   = regexp.MustCompile(`^Round:\s*(\d+)/(\d+)\s*$`)
)

// ParseRoundMarker reads the review-round counter of an orchestrator comment: the canonical
// `[orchestrator] ROUND n/N` at the start of the body, or a `Round: n/N` line anywhere in a
// body that starts with `[orchestrator]`. 1 <= n <= N <= 99, else ok is false. The caller must
// only pass comments written by an API token: a human comment never sets a round.
func ParseRoundMarker(body string) (n, max int, ok bool) {
	body = strings.ReplaceAll(body, "\r\n", "\n")
	first := body
	if i := strings.IndexByte(body, '\n'); i >= 0 {
		first = body[:i]
	}
	if m := roundTagged.FindStringSubmatch(first); m != nil {
		return roundValues(m[1], m[2])
	}
	if !strings.HasPrefix(body, "[orchestrator]") {
		return 0, 0, false
	}
	for _, line := range strings.Split(body, "\n") {
		if m := roundLine.FindStringSubmatch(line); m != nil {
			return roundValues(m[1], m[2])
		}
	}
	return 0, 0, false
}

func roundValues(a, b string) (int, int, bool) {
	n, err1 := strconv.Atoi(a)
	max, err2 := strconv.Atoi(b)
	if err1 != nil || err2 != nil || n < 1 || max < 1 || max > maxRoundCap || n > max {
		return 0, 0, false
	}
	return n, max, true
}
