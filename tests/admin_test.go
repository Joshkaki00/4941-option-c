package tests

import (
	"strings"
	"testing"

	"github.com/Joshkaki00/4941-option-c/internal/admin"
)

func TestSourcePathAcceptsRelativeMarkdown(t *testing.T) {
	for _, p := range []string{"pto-policy.md", "hr/leave.md", "deep/er/x.md", "Notes.MD", "a.b.md"} {
		if !admin.ValidSourcePath(p) {
			t.Errorf("%q should be accepted", p)
		}
	}
}

func TestSourcePathRejectsTraversalAndAbsolute(t *testing.T) {
	for _, p := range []string{
		"../etc/passwd.md",
		"a/../../b.md",
		"sub/../x.md",
		"./x.md",
		"a//b.md",
		"/abs/x.md",
		"a/./b.md",
	} {
		if admin.ValidSourcePath(p) {
			t.Errorf("%q must be rejected", p)
		}
	}
}

func TestSourcePathRejectsNonMarkdownAndControlCharacters(t *testing.T) {
	for _, p := range []string{"", "notes.txt", "x.md.exe", "x.md ", "back\\slash.md", "nul\x00.md", "x"} {
		if admin.ValidSourcePath(p) {
			t.Errorf("%q must be rejected", p)
		}
	}
}

func TestSourcePathLengthLimit(t *testing.T) {
	// 252 characters plus ".md" is 255: the largest accepted path.
	if !admin.ValidSourcePath(strings.Repeat("a", 252) + ".md") {
		t.Fatal("255-char path should be accepted")
	}
	if admin.ValidSourcePath(strings.Repeat("a", 253) + ".md") {
		t.Fatal("256-char path must be rejected")
	}
}
