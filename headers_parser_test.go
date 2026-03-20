package staticadapter

import (
	"strings"
	"testing"
)

func TestParseBasic(t *testing.T) {
	input := `/secure/page
  X-Frame-Options: DENY
  X-Content-Type-Options: nosniff
  Referrer-Policy: no-referrer
/static/*
  Access-Control-Allow-Origin: *
  X-Robots-Tag: nosnippet
`

	rules, warnings := Parse(strings.NewReader(input), 0, 0, 0)
	if len(warnings) > 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if len(rules) != 2 {
		t.Fatalf("expected 2 rules, got %d", len(rules))
	}

	// First rule
	if rules[0].Pattern != "/secure/page" {
		t.Errorf("expected pattern /secure/page, got %s", rules[0].Pattern)
	}
	if len(rules[0].Ops) != 3 {
		t.Fatalf("expected 3 ops for first rule, got %d", len(rules[0].Ops))
	}
	if rules[0].Ops[0].Name != "X-Frame-Options" || rules[0].Ops[0].Value != "DENY" {
		t.Errorf("unexpected first op: %+v", rules[0].Ops[0])
	}

	// Second rule
	if rules[1].Pattern != "/static/*" {
		t.Errorf("expected pattern /static/*, got %s", rules[1].Pattern)
	}
	if len(rules[1].Ops) != 2 {
		t.Fatalf("expected 2 ops for second rule, got %d", len(rules[1].Ops))
	}
}

func TestParseComments(t *testing.T) {
	input := `# This is a comment
/page
  X-Test: value
# Another comment

/other
  X-Other: value2
`

	rules, warnings := Parse(strings.NewReader(input), 0, 0, 0)
	if len(warnings) > 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if len(rules) != 2 {
		t.Fatalf("expected 2 rules, got %d", len(rules))
	}
}

func TestParseBlankLines(t *testing.T) {
	input := `

/page
  X-Test: value


/other
  X-Other: value2

`

	rules, warnings := Parse(strings.NewReader(input), 0, 0, 0)
	if len(warnings) > 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if len(rules) != 2 {
		t.Fatalf("expected 2 rules, got %d", len(rules))
	}
}

func TestParseRemoveHeader(t *testing.T) {
	input := `/*
  Content-Security-Policy: default-src 'self';
/*.jpg
  ! Content-Security-Policy
`

	rules, warnings := Parse(strings.NewReader(input), 0, 0, 0)
	if len(warnings) > 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if len(rules) != 2 {
		t.Fatalf("expected 2 rules, got %d", len(rules))
	}

	if rules[1].Ops[0].Mode != OpRemove {
		t.Errorf("expected remove op, got %v", rules[1].Ops[0].Mode)
	}
	if rules[1].Ops[0].Name != "Content-Security-Policy" {
		t.Errorf("expected Content-Security-Policy, got %s", rules[1].Ops[0].Name)
	}
}

func TestParseAbsoluteURL(t *testing.T) {
	input := `https://myworker.mysubdomain.workers.dev/*
  X-Robots-Tag: noindex
`

	rules, warnings := Parse(strings.NewReader(input), 0, 0, 0)
	if len(warnings) > 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}
	if rules[0].Pattern != "https://myworker.mysubdomain.workers.dev/*" {
		t.Errorf("unexpected pattern: %s", rules[0].Pattern)
	}
}

func TestParseInvalidAbsoluteURL(t *testing.T) {
	input := `http://example.com/*
  X-Test: value
`
	rules, warnings := Parse(strings.NewReader(input), 0, 0, 0)
	// Pattern is rejected (not https://), so the header line also gets a warning
	// because there's no current rule.
	if len(warnings) < 1 {
		t.Fatalf("expected at least 1 warning, got %d", len(warnings))
	}
	if len(rules) != 0 {
		t.Fatalf("expected 0 rules, got %d", len(rules))
	}
}

func TestParseMaxRulesLimit(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 5; i++ {
		sb.WriteString("/path" + string(rune('a'+i)) + "\n")
		sb.WriteString("  X-Test: value\n")
	}

	rules, warnings := Parse(strings.NewReader(sb.String()), 3, 0, 0)
	if len(rules) != 3 {
		t.Fatalf("expected 3 rules (limited), got %d", len(rules))
	}
	if len(warnings) == 0 {
		t.Fatal("expected warnings about exceeding max rules")
	}
}

func TestParseHeaderWithoutPattern(t *testing.T) {
	input := `  X-Test: value
`
	rules, warnings := Parse(strings.NewReader(input), 0, 0, 0)
	if len(rules) != 0 {
		t.Fatalf("expected 0 rules, got %d", len(rules))
	}
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d", len(warnings))
	}
}

func TestParseInvalidHeaderName(t *testing.T) {
	input := `/test
  Invalid Header: value
`
	rules, warnings := Parse(strings.NewReader(input), 0, 0, 0)
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}
	// The space in "Invalid Header" makes it invalid.
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning for invalid header name, got %d", len(warnings))
	}
	if len(rules[0].Ops) != 0 {
		t.Fatalf("expected 0 ops, got %d", len(rules[0].Ops))
	}
}

func TestParseMissingColon(t *testing.T) {
	input := `/test
  NoColonHere
`
	rules, warnings := Parse(strings.NewReader(input), 0, 0, 0)
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning for missing colon, got %d", len(warnings))
	}
}

func TestParseMultipleWildcards(t *testing.T) {
	input := `/*/*.jpg
  X-Test: value
`
	rules, warnings := Parse(strings.NewReader(input), 0, 0, 0)
	if len(rules) != 0 {
		t.Fatalf("expected 0 rules (multiple wildcards rejected), got %d", len(rules))
	}
	// Pattern rejected + header line has no current rule = 2 warnings.
	if len(warnings) < 1 {
		t.Fatalf("expected at least 1 warning, got %d", len(warnings))
	}
}

func TestParsePlaceholders(t *testing.T) {
	input := `/movies/:title
  x-movie-name: You are watching ":title"
`
	rules, warnings := Parse(strings.NewReader(input), 0, 0, 0)
	if len(warnings) > 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}
	if rules[0].Ops[0].Value != `You are watching ":title"` {
		t.Errorf("unexpected value: %s", rules[0].Ops[0].Value)
	}
}

func TestParseTabIndentation(t *testing.T) {
	input := "/page\n\tX-Test: value\n"
	rules, warnings := Parse(strings.NewReader(input), 0, 0, 0)
	if len(warnings) > 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if len(rules) != 1 || len(rules[0].Ops) != 1 {
		t.Fatalf("expected 1 rule with 1 op, got %d rules", len(rules))
	}
}

// ---------- Security Tests ----------

func TestParseNonASCIIHeaderName(t *testing.T) {
	// RFC 7230 requires header names to be US-ASCII only.
	// Non-ASCII Unicode letters should be rejected.
	input := "/test\n  Ünvalid: value\n"
	rules, warnings := Parse(strings.NewReader(input), 0, 0, 0)
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning for non-ASCII header name, got %d", len(warnings))
	}
	if len(rules[0].Ops) != 0 {
		t.Fatalf("expected 0 ops (non-ASCII rejected), got %d", len(rules[0].Ops))
	}
}

func TestParseCRLFInHeaderValue(t *testing.T) {
	input := "/test\n  X-Bad: value\r\nInjected: header\n"
	// The \r\n in the value should be caught by the scanner splitting on lines,
	// so this is more about the line-level parsing.
	rules, _ := Parse(strings.NewReader(input), 0, 0, 0)
	for _, rule := range rules {
		for _, op := range rule.Ops {
			if strings.ContainsAny(op.Value, "\r\n") {
				t.Errorf("CRLF found in header value: %q", op.Value)
			}
		}
	}
}

func TestParseLineLength(t *testing.T) {
	longLine := "/test\n  X-Test: " + strings.Repeat("a", HeaderMaxLineLength+1) + "\n"
	_, warnings := Parse(strings.NewReader(longLine), 0, 0, 0)
	found := false
	for _, w := range warnings {
		if strings.Contains(w.Error(), "exceeds maximum length") {
			found = true
		}
	}
	if !found {
		t.Error("expected warning about line length")
	}
}

func TestParseCustomMaxLineLength(t *testing.T) {
	// A line that exceeds the default HeaderMaxLineLength (2000) but fits within a custom limit.
	longValue := strings.Repeat("a", HeaderMaxLineLength+500)
	input := "/test\n  X-Test: " + longValue + "\n"

	// With default limit, parsing should warn and skip the long line.
	rules, warnings := Parse(strings.NewReader(input), 0, 0, 0)
	if len(warnings) == 0 {
		t.Fatal("expected warning with default max line length")
	}
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}
	if len(rules[0].Ops) != 0 {
		t.Fatalf("expected 0 ops (long line skipped), got %d", len(rules[0].Ops))
	}

	// With a larger custom limit, the line should be accepted.
	rules, warnings = Parse(strings.NewReader(input), 0, 0, HeaderMaxLineLength+1000)
	if len(warnings) > 0 {
		t.Fatalf("unexpected warnings with custom max line length: %v", warnings)
	}
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}
	if len(rules[0].Ops) != 1 {
		t.Fatalf("expected 1 op with custom limit, got %d", len(rules[0].Ops))
	}
	if rules[0].Ops[0].Value != longValue {
		t.Errorf("expected long value to be preserved, got length %d", len(rules[0].Ops[0].Value))
	}
}

// ---------- Edge Case Tests ----------

func TestEmptyFile(t *testing.T) {
	rules, warnings := Parse(strings.NewReader(""), 0, 0, 0)
	if len(rules) != 0 {
		t.Fatalf("expected 0 rules for empty file, got %d", len(rules))
	}
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings for empty file, got %d", len(warnings))
	}
}

func TestOnlyComments(t *testing.T) {
	input := "# comment 1\n# comment 2\n"
	rules, warnings := Parse(strings.NewReader(input), 0, 0, 0)
	if len(rules) != 0 {
		t.Fatalf("expected 0 rules, got %d", len(rules))
	}
	if len(warnings) != 0 {
		t.Fatalf("expected 0 warnings, got %d", len(warnings))
	}
}

func TestRuleWithNoHeaders(t *testing.T) {
	input := "/page\n/other\n  X-Test: value\n"
	rules, warnings := Parse(strings.NewReader(input), 0, 0, 0)
	if len(warnings) > 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if len(rules) != 2 {
		t.Fatalf("expected 2 rules, got %d", len(rules))
	}
	if len(rules[0].Ops) != 0 {
		t.Errorf("expected 0 ops for first rule, got %d", len(rules[0].Ops))
	}
	if len(rules[1].Ops) != 1 {
		t.Errorf("expected 1 op for second rule, got %d", len(rules[1].Ops))
	}
}

func TestHeaderValueWithColons(t *testing.T) {
	// Header values may contain colons (e.g., CSP directives).
	input := `/page
  Content-Security-Policy: script-src 'self'; frame-ancestors 'none';
`
	rules, warnings := Parse(strings.NewReader(input), 0, 0, 0)
	if len(warnings) > 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if rules[0].Ops[0].Value != "script-src 'self'; frame-ancestors 'none';" {
		t.Errorf("unexpected value: %s", rules[0].Ops[0].Value)
	}
}
