package service

import (
	"strings"
	"testing"
)

func sp(s string) *string { return &s }

func TestParseDescriptionLinks(t *testing.T) {
	long := "https://github.com/o/r/pull/" + strings.Repeat("1", 500)
	var many []string
	for i := 0; i < 50; i++ {
		many = append(many, "filler")
	}
	cases := []struct {
		name, desc string
		pr, branch *string
	}{
		{"empty", "", nil, nil},
		{"both", "Intro\nPR: https://github.com/o/r/pull/12\nBranch: feat/x-1\n", sp("https://github.com/o/r/pull/12"), sp("feat/x-1")},
		{"http dropped", "PR: http://github.com/o/r/pull/1", nil, nil},
		{"javascript dropped", "PR: javascript:alert(1)", nil, nil},
		{"duplicate first wins", "PR: https://a.example/1\nPR: https://b.example/2", sp("https://a.example/1"), nil},
		{"duplicate invalid first still wins", "PR: http://a.example/1\nPR: https://b.example/2", nil, nil},
		{"key mid line", "see PR: https://a.example/1", nil, nil},
		{"lowercase key", "pr: https://a.example/1", nil, nil},
		{"crlf", "PR: https://a.example/1\r\nBranch: b-1\r\n", sp("https://a.example/1"), sp("b-1")},
		{"too long url", "PR: " + long, nil, nil},
		{"line 51 ignored", strings.Join(many, "\n") + "\nPR: https://a.example/1", nil, nil},
		{"line 50 counts", strings.Join(many[:49], "\n") + "\nPR: https://a.example/1", sp("https://a.example/1"), nil},
		{"whitespace in value", "Branch: a b", nil, nil},
		{"branch too long", "Branch: " + strings.Repeat("a", 201), nil, nil},
		{"branch 200 ok", "Branch: " + strings.Repeat("a", 200), nil, sp(strings.Repeat("a", 200))},
		{"trailing spaces", "Branch:   feat/a   \n", nil, sp("feat/a")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			pr, br := ParseDescriptionLinks(c.desc)
			if !eqp(pr, c.pr) || !eqp(br, c.branch) {
				t.Fatalf("got pr=%v branch=%v, want pr=%v branch=%v", deref(pr), deref(br), deref(c.pr), deref(c.branch))
			}
		})
	}
}

func eqp(a, b *string) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

func deref(p *string) string {
	if p == nil {
		return "<nil>"
	}
	return *p
}

func TestParseRoundMarker(t *testing.T) {
	cases := []struct {
		name, body string
		n, max     int
		ok         bool
	}{
		{"tagged", "[orchestrator] ROUND 2/3", 2, 3, true},
		{"tagged with text", "[orchestrator] ROUND 1/3\nSending back to dev", 1, 3, true},
		{"round line", "[orchestrator] STATUS\nRound: 2/3\nmore", 2, 3, true},
		{"round line crlf", "[orchestrator] STATUS\r\nRound: 3/3\r\n", 3, 3, true},
		{"non numeric", "[orchestrator] ROUND x/3", 0, 0, false},
		{"n above max", "[orchestrator] ROUND 4/3", 0, 0, false},
		{"zero", "[orchestrator] ROUND 0/3", 0, 0, false},
		{"max above 99", "[orchestrator] ROUND 1/100", 0, 0, false},
		{"no marker", "[orchestrator] PLAN", 0, 0, false},
		{"wrong role tag", "[developer] ROUND 1/3", 0, 0, false},
		{"plain text", "Round 1/3", 0, 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			n, max, ok := ParseRoundMarker(c.body)
			if ok != c.ok || n != c.n || max != c.max {
				t.Fatalf("got %d/%d ok=%v", n, max, ok)
			}
		})
	}
}

func TestGateLabels(t *testing.T) {
	if l, ok := GateLabel(GateRequirements); !ok || l != "awaiting-req-approval" {
		t.Fatal(l, ok)
	}
	if l, ok := GateLabel(GatePlan); !ok || l != "awaiting-plan-approval" {
		t.Fatal(l, ok)
	}
	if _, ok := GateLabel("merge"); ok {
		t.Fatal("merge is not a decision gate")
	}
}
