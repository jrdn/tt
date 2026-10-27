package api

import (
	"strings"
	"testing"
)

func TestMDToHTMLLinkifiesBareURLs(t *testing.T) {
	got := mdToHTML("see https://tt.j13g.party/?project=tt for details")
	if !strings.Contains(got, `<a href="https://tt.j13g.party/?project=tt">https://tt.j13g.party/?project=tt</a>`) {
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
	if !strings.Contains(got, `<a href="https://example.com/docs">docs</a>`) {
		t.Fatalf("markdown link not rendered: %q", got)
	}
}

func TestMDToHTMLPlainTextStaysPlain(t *testing.T) {
	got := mdToHTML("a plain sentence with no links")
	if strings.Contains(got, "<a ") {
		t.Fatalf("plain text gained an anchor: %q", got)
	}
}
