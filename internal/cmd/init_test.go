package cmd

import (
	"strings"
	"testing"
)

func TestInjectSection_EmptyFile(t *testing.T) {
	result, inserted := injectSection("")
	if !inserted {
		t.Fatal("expected inserted=true for empty file")
	}
	if !strings.Contains(result, sectionHeader) {
		t.Error("result missing section header")
	}
	if !strings.Contains(result, sectionEnd) {
		t.Error("result missing end marker")
	}
}

func TestInjectSection_FileWithNoSection(t *testing.T) {
	existing := "# My Project\n\nSome notes.\n"
	result, inserted := injectSection(existing)
	if !inserted {
		t.Fatal("expected inserted=true")
	}
	if !strings.HasPrefix(result, existing) {
		t.Error("existing content should be preserved at top")
	}
	if !strings.Contains(result, sectionHeader) {
		t.Error("result missing section header")
	}
}

func TestInjectSection_FileWithExistingSection(t *testing.T) {
	existing := "# My Project\n\n" + injectedContent + "\n\n## Other Section\n"
	result, inserted := injectSection(existing)
	if inserted {
		t.Fatal("expected inserted=false when section already present")
	}
	// Should still have one copy of the header
	count := strings.Count(result, sectionHeader)
	if count != 1 {
		t.Errorf("expected 1 copy of section header, got %d", count)
	}
	// Content after section should be preserved
	if !strings.Contains(result, "## Other Section") {
		t.Error("content after section should be preserved")
	}
}

func TestInjectSection_PreservesContentBefore(t *testing.T) {
	before := "# Agent Instructions\n\nDo stuff.\n"
	existing := before + "\n" + injectedContent + "\n"
	result, inserted := injectSection(existing)
	if inserted {
		t.Fatal("expected inserted=false")
	}
	if !strings.HasPrefix(result, before) {
		t.Errorf("content before section not preserved\ngot: %q", result[:len(before)+20])
	}
}

func TestInjectSection_PreservesContentAfter(t *testing.T) {
	after := "\n## Other Section\n\nMore stuff.\n"
	existing := injectedContent + "\n" + after
	result, _ := injectSection(existing)
	if !strings.HasSuffix(strings.TrimRight(result, "\n"), strings.TrimRight(after, "\n")) {
		t.Errorf("content after section not preserved\nresult: %q", result)
	}
}

func TestInjectSection_MissingEndMarker(t *testing.T) {
	// BEGIN present but END missing — should replace to EOF
	existing := "# Notes\n\n" + sectionHeader + "\n\nSome stale content.\n"
	result, inserted := injectSection(existing)
	if inserted {
		t.Fatal("expected inserted=false when header found")
	}
	if !strings.Contains(result, sectionEnd) {
		t.Error("result should contain end marker after replacement")
	}
	if strings.Contains(result, "Some stale content.") {
		t.Error("stale content should have been replaced")
	}
}

func TestInjectSection_IdempotentContent(t *testing.T) {
	first, _ := injectSection("")
	second, _ := injectSection(first)
	if first != second {
		t.Error("second injection should produce identical output")
	}
}

func TestInitServerProject(t *testing.T) {
	cases := []struct {
		name              string
		envSrv, envProj   string
		flagSrv, flagProj string
		wantSrv, wantProj string
	}{
		{"flags win over env", "https://env", "envp", "https://flag", "flagp", "https://flag", "flagp"},
		{"env used when no flags", "https://env", "envp", "", "", "https://env", "envp"},
		{"env needs both", "https://env", "", "", "", "", ""},
		{"partial flag is left for the caller to reject", "https://env", "envp", "https://flag", "", "https://flag", ""},
		{"nothing set", "", "", "", "", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("TT_SERVER", c.envSrv)
			t.Setenv("TT_PROJECT", c.envProj)
			srv, proj := initServerProject(c.flagSrv, c.flagProj)
			if srv != c.wantSrv || proj != c.wantProj {
				t.Errorf("got (%q, %q), want (%q, %q)", srv, proj, c.wantSrv, c.wantProj)
			}
		})
	}
}
