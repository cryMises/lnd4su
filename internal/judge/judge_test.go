package judge

import (
	"testing"

	"github.com/crymises/lnd4su/internal/store"
)

func TestNormalize(t *testing.T) {
	cases := []struct {
		got, want string
		eq        bool
	}{
		{"1\r\n2\r\n", "1\n2", true},
		{"1\n2\n\n\n", "1\n2", true},
		{"1 \n2\t\n", "1\n2", true},
		{"1\n2", "1\n3", false},
		{" 1\n2", "1\n2", false}, // leading whitespace is significant
	}
	for _, c := range cases {
		if compare(c.got, c.want, "tolerant") != c.eq {
			t.Errorf("tolerant %q vs %q = %v, want %v", c.got, c.want, !c.eq, c.eq)
		}
	}
	if !compare("a\n", "a\n", "exact") {
		t.Error("exact identity")
	}
	if compare("a\r\n", "a\n", "exact") {
		t.Error("exact must be byte-identical")
	}
}

func TestOverallVerdict(t *testing.T) {
	ac := func(n int) []store.CaseResult {
		r := make([]store.CaseResult, n)
		for i := range r {
			r[i].Verdict = "AC"
		}
		return r
	}
	r := ac(10)
	if overallVerdict(r) != "AC" {
		t.Fatal("all AC -> AC")
	}
	r[3].Verdict = "WA"
	r[7].Verdict = "TLE"
	if overallVerdict(r) != "WA" {
		t.Fatal("first non-passing verdict wins")
	}
	r[0].Verdict = "MLE"
	if overallVerdict(r) != "MLE" {
		t.Fatal("earliest non-passing verdict wins")
	}
}
