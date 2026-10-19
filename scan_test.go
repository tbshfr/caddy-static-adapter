package staticadapter

import (
	"strings"
	"testing"
)

func TestParseRedirectsVeryLongLineDoesNotDropRest(t *testing.T) {
	input := "/a /x 301\n/" + strings.Repeat("b", 10*RedirectMaxLineLength) + " /y 301\n/c /z 301\n"
	rules, warnings := ParseRedirects(strings.NewReader(input), 0, 0, 0)

	if len(rules) != 2 || rules[0].From != "/a" || rules[1].From != "/c" {
		t.Fatalf("expected rules /a and /c around the long line, got %+v", rules)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0].Error(), "line 2: line exceeds maximum length") {
		t.Fatalf("expected one line-length warning for line 2, got %v", warnings)
	}
}

func TestParseHeadersVeryLongLineDoesNotDropRest(t *testing.T) {
	input := "/a\n  X-A: " + strings.Repeat("v", 10*HeaderMaxLineLength) + "\n  X-B: kept\n/c\n  X-C: kept\n"
	rules, warnings := Parse(strings.NewReader(input), 0, 0, 0)

	if len(rules) != 2 {
		t.Fatalf("expected 2 rules, got %d", len(rules))
	}
	if len(rules[0].Ops) != 1 || rules[0].Ops[0].Name != "X-B" {
		t.Errorf("expected only X-B on /a, got %+v", rules[0].Ops)
	}
	if len(rules[1].Ops) != 1 || rules[1].Ops[0].Name != "X-C" {
		t.Errorf("expected X-C on /c, got %+v", rules[1].Ops)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0].Error(), "line 2: line exceeds maximum length") {
		t.Fatalf("expected one line-length warning for line 2, got %v", warnings)
	}
}

func TestScanLinesFileSizeBoundary(t *testing.T) {
	collect := func(input string, maxFileSize int) ([]string, []error) {
		var lines []string
		var warnings []error
		scanLines(strings.NewReader(input), maxFileSize, 100, &warnings, func(_ int, line string) bool {
			lines = append(lines, line)
			return false
		})
		return lines, warnings
	}

	// Exactly at the limit, with and without a trailing newline.
	for _, input := range []string{"/a /x\n/b /y", "/a /x\n/b /y\n"} {
		lines, warnings := collect(input, len(input))
		if len(warnings) != 0 || len(lines) != 2 {
			t.Errorf("%q at limit: expected 2 lines and no warnings, got %q, %v", input, lines, warnings)
		}
	}

	// One byte over: the lines that fit are kept, the rest is rejected.
	input := "/a /x\n/b /y\n"
	lines, warnings := collect(input, len(input)-1)
	if len(lines) != 1 || lines[0] != "/a /x" {
		t.Errorf("over limit: expected only the first line, got %q", lines)
	}
	if len(warnings) != 1 || !strings.Contains(warnings[0].Error(), "line 2: file exceeds maximum size") {
		t.Errorf("over limit: expected size warning on line 2, got %v", warnings)
	}
}

func TestScanLinesCRLF(t *testing.T) {
	var lines []string
	var warnings []error
	scanLines(strings.NewReader("/a /x\r\n12345\r\n/c /z"), 1000, 5, &warnings, func(_ int, line string) bool {
		lines = append(lines, line)
		return false
	})

	want := []string{"/a /x", "12345", "/c /z"}
	if strings.Join(lines, "|") != strings.Join(want, "|") {
		t.Errorf("expected %q, got %q", want, lines)
	}
	if len(warnings) != 0 {
		t.Errorf("CRLF must not count toward line length, got %v", warnings)
	}
}
