package api

import (
	"strings"
	"testing"
)

func TestMDToHTMLLinkifiesBareURLs(t *testing.T) {
	got := mdToHTML("see https://tt.j13g.party/?project=tt for details")
	if !strings.Contains(got, `href="https://tt.j13g.party/?project=tt"`) {
		t.Fatalf("bare URL not linkified: %q", got)
	}
}

func TestMDToHTMLLinkifiesURLsInComments(t *testing.T) {
	got := mdToHTML("PR https://github.com/jrdn/tt/pull/8 is up")
	if !strings.Contains(got, `href="https://github.com/jrdn/tt/pull/8"`) {
		t.Fatalf("URL in prose not linkified: %q", got)
	}
}

func TestMDToHTMLKeepsMarkdownLinks(t *testing.T) {
	got := mdToHTML("[docs](https://example.com/docs)")
	if !strings.Contains(got, `href="https://example.com/docs"`) ||
		!strings.Contains(got, ">docs</a>") {
		t.Fatalf("markdown link not rendered: %q", got)
	}
}

func TestMDToHTMLLinksOpenInNewTab(t *testing.T) {
	got := mdToHTML("see https://example.com/a and [docs](https://example.com/docs)")
	if n := strings.Count(got, `target="_blank"`); n != 2 {
		t.Fatalf("want 2 links opening in a new tab, got %d: %q", n, got)
	}
	if !strings.Contains(got, `rel="noopener noreferrer"`) {
		t.Fatalf("link lost rel=noopener noreferrer: %q", got)
	}
}

func TestMDToHTMLRendersTables(t *testing.T) {
	got := mdToHTML("| id | title |\n| -- | ----- |\n| ab | one   |")
	if !strings.Contains(got, "<table>") || !strings.Contains(got, "<td>one</td>") {
		t.Fatalf("GFM table not rendered: %q", got)
	}
}

func TestMDToHTMLRendersStrikethroughAndTaskLists(t *testing.T) {
	got := mdToHTML("~~old~~\n\n- [ ] todo\n- [x] done")
	if !strings.Contains(got, "<del>old</del>") {
		t.Fatalf("strikethrough not rendered: %q", got)
	}
	if !strings.Contains(got, `type="checkbox"`) {
		t.Fatalf("task list not rendered: %q", got)
	}
}

func TestMDToHTMLPlainTextStaysPlain(t *testing.T) {
	got := mdToHTML("a plain sentence with no links")
	if strings.Contains(got, "<a ") {
		t.Fatalf("plain text gained an anchor: %q", got)
	}
}
