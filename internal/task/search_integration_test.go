//go:build integration

package task

import (
	"math/rand"
	"slices"
	"sort"
	"testing"

	"github.com/jmoiron/sqlx"
)

// searchReference is Search as it was before candidates were filtered in
// SQL (load everything, score in Go), kept to check the two agree.
func searchReference(db *sqlx.DB, query string) ([]Task, error) {
	var all []Task
	if err := db.Select(&all, `SELECT * FROM tasks ORDER BY `+insertOrder(db)); err != nil {
		return nil, err
	}

	type entry struct {
		task  Task
		score int
	}
	var results []entry

	for _, t := range all {
		matched := false
		score := 0

		if ok, s := fuzzyScore(query, t.Title); ok {
			s += 100
			if !matched || s > score {
				score = s
			}
			matched = true
		}
		if t.Description != nil {
			if ok, s := fuzzyScore(query, *t.Description); ok {
				s += 50
				if !matched || s > score {
					score = s
				}
				matched = true
			}
		}

		if t.Assignee != nil {
			if ok, s := fuzzyScore(query, *t.Assignee); ok {
				s += 80
				if !matched || s > score {
					score = s
				}
				matched = true
			}
		}

		var comments []Comment
		_ = db.Select(&comments, db.Rebind(`SELECT * FROM comments WHERE task_id=?`), t.ID)
		for _, c := range comments {
			if ok, s := fuzzyScore(query, c.Body); ok {
				if !matched || s > score {
					score = s
				}
				matched = true
			}
			if c.Author != nil {
				if ok, s := fuzzyScore(query, *c.Author); ok {
					if !matched || s > score {
						score = s
					}
					matched = true
				}
			}
		}

		if matched {
			results = append(results, entry{t, score})
		}
	}

	sort.SliceStable(results, func(i, j int) bool {
		if results[i].score != results[j].score {
			return results[i].score > results[j].score
		}
		if results[i].task.Priority != results[j].task.Priority {
			return results[i].task.Priority < results[j].task.Priority
		}
		return results[i].task.CreatedAt < results[j].task.CreatedAt
	})

	out := make([]Task, len(results))
	for i, r := range results {
		out[i] = r.task
	}
	return out, nil
}

func TestSearch_MatchesReference(t *testing.T) {
	d := openTestDB(t)
	rng := rand.New(rand.NewSource(1))
	words := []string{"Énergie", "straße", "deploy", "Deploy", "auth", "AUTH", "50%", "a_b", `back\slash`,
		"bear", "risk", "brisk", "ok", "Zoë", "naïve", "server", "sync", "tt", "x_y%z"}
	pick := func(n int) string {
		s := ""
		for i := 0; i < n; i++ {
			if i > 0 {
				s += " "
			}
			s += words[rng.Intn(len(words))]
		}
		return s
	}
	for i := 0; i < 120; i++ {
		opts := CreateOpts{}
		if rng.Intn(2) == 0 {
			desc := pick(4)
			opts.Description = &desc
		}
		if rng.Intn(3) == 0 {
			a := []string{"alice", "Bob", "claude/opus4.7", "Ünal"}[rng.Intn(4)]
			opts.Assignee = &a
		}
		tk, err := Create(d, pick(1+rng.Intn(3)), opts)
		if err != nil {
			t.Fatal(err)
		}
		for j := rng.Intn(3); j > 0; j-- {
			author := []string{"jrdn", "Mallory", "claude/opus4.7"}[rng.Intn(3)]
			AddComment(d, tk.ID, pick(3), &author)
		}
	}

	queries := []string{"", "e", "é", "É", "énergie", "STRASSE", "straße", "dpl", "brsk", "risk", "%", "_", `\`,
		"50%", "a_b", "x_y", "zoë", "ZOË", "naive", "ünal", "claude", "mallory", "opus", "nomatch", "b%k", "a\\"}
	for i := 0; i < 40; i++ { // random subsequences drawn from the vocabulary
		w := []rune(words[rng.Intn(len(words))])
		var q []rune
		for _, r := range w {
			if rng.Intn(2) == 0 {
				q = append(q, r)
			}
		}
		queries = append(queries, string(q))
	}

	for _, q := range queries {
		got, err := Search(d, q)
		if err != nil {
			t.Fatalf("Search(%q): %v", q, err)
		}
		want, err := searchReference(d, q)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(ids(got), ids(want)) {
			t.Errorf("Search(%q) = %d results %v\nreference   %d results %v", q, len(got), ids(got), len(want), ids(want))
		}
	}
}

func ids(ts []Task) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.ID
	}
	return out
}
