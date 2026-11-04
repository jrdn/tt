package task

import "testing"

func TestIDScore(t *testing.T) {
	cases := []struct {
		name  string
		query string
		id    string
		want  bool
		score int
	}{
		{"exact", "4323pyg", "4323pyg", true, 400},
		{"exact upper", "4323PYG", "4323pyg", true, 400},
		{"exact padded", " 4323pyg ", "4323pyg", true, 400},
		{"prefix", "4323", "4323pyg", true, 300},
		{"prefix one char", "4", "4323pyg", true, 300},
		{"substring", "23py", "4323pyg", true, 200},
		{"subsequence only", "4yg", "4323pyg", false, 0},
		{"empty", "", "4323pyg", false, 0},
		{"other id", "4323pyg", "a0ssm4f", false, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ok, score := idScore(c.query, c.id)
			if ok != c.want || score != c.score {
				t.Fatalf("idScore(%q, %q) = (%v, %d), want (%v, %d)",
					c.query, c.id, ok, score, c.want, c.score)
			}
		})
	}
}

func TestSubstringPattern(t *testing.T) {
	cases := map[string]string{
		"":     "%%",
		"43":   "%43%",
		"ABCD": "%abcd%",
		"50%":  `%50\%%`,
		"a_b":  `%a\_b%`,
		`a\b`:  `%a\\b%`,
	}
	for query, want := range cases {
		if got := substringPattern(query); got != want {
			t.Errorf("substringPattern(%q) = %q, want %q", query, got, want)
		}
	}
}
