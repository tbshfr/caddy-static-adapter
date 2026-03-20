package staticadapter

import (
	"fmt"
	"strings"
	"testing"
)

func TestParseRedirectsBasic(t *testing.T) {
	input := `/home /index.html
/blog/old /blog/new 301
`
	rules, warnings := ParseRedirects(strings.NewReader(input), 0, 0, 0)
	if len(warnings) > 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if len(rules) != 2 {
		t.Fatalf("expected 2 rules, got %d", len(rules))
	}

	if rules[0].From != "/home" || rules[0].To != "/index.html" || rules[0].Status != 302 {
		t.Errorf("unexpected rule 0: %+v", rules[0])
	}
	if rules[1].From != "/blog/old" || rules[1].To != "/blog/new" || rules[1].Status != 301 {
		t.Errorf("unexpected rule 1: %+v", rules[1])
	}
}

func TestParseRedirectsDefaultStatus(t *testing.T) {
	input := `/old /new
`
	rules, warnings := ParseRedirects(strings.NewReader(input), 0, 0, 0)
	if len(warnings) > 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}
	if rules[0].Status != 302 {
		t.Errorf("expected default status 302, got %d", rules[0].Status)
	}
}

func TestParseRedirectsComments(t *testing.T) {
	input := `# Redirect old pages
/old /new 301
# Another comment

/foo /bar
`
	rules, warnings := ParseRedirects(strings.NewReader(input), 0, 0, 0)
	if len(warnings) > 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if len(rules) != 2 {
		t.Fatalf("expected 2 rules, got %d", len(rules))
	}
}

func TestParseRedirectsBlankLines(t *testing.T) {
	input := `

/old /new 301

/foo /bar 302

`
	rules, warnings := ParseRedirects(strings.NewReader(input), 0, 0, 0)
	if len(warnings) > 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if len(rules) != 2 {
		t.Fatalf("expected 2 rules, got %d", len(rules))
	}
}

func TestParseRedirectsAbsoluteDestination(t *testing.T) {
	input := `/google https://www.google.com 301
`
	rules, warnings := ParseRedirects(strings.NewReader(input), 0, 0, 0)
	if len(warnings) > 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}
	if rules[0].To != "https://www.google.com" {
		t.Errorf("expected https://www.google.com, got %s", rules[0].To)
	}
}

func TestParseRedirectsHTTPDestination(t *testing.T) {
	input := `/old http://example.com/new 301
`
	rules, warnings := ParseRedirects(strings.NewReader(input), 0, 0, 0)
	if len(warnings) > 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}
	if rules[0].To != "http://example.com/new" {
		t.Errorf("expected http://example.com/new, got %s", rules[0].To)
	}
}

func TestParseRedirectsWildcard(t *testing.T) {
	input := `/blog/* /news/:splat 301
`
	rules, warnings := ParseRedirects(strings.NewReader(input), 0, 0, 0)
	if len(warnings) > 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}
	if rules[0].From != "/blog/*" {
		t.Errorf("expected /blog/*, got %s", rules[0].From)
	}
}

func TestParseRedirectsPlaceholders(t *testing.T) {
	input := `/products/:code/:name /items?code=:code&name=:name 301
`
	rules, warnings := ParseRedirects(strings.NewReader(input), 0, 0, 0)
	if len(warnings) > 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}
}

func TestParseRedirectsInvalidStatusCode(t *testing.T) {
	input := `/old /new 404
`
	rules, warnings := ParseRedirects(strings.NewReader(input), 0, 0, 0)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d", len(warnings))
	}
	if len(rules) != 0 {
		t.Fatalf("expected 0 rules, got %d", len(rules))
	}
}

func TestParseRedirectsInvalidStatusCodeNonNumeric(t *testing.T) {
	input := `/old /new abc
`
	rules, warnings := ParseRedirects(strings.NewReader(input), 0, 0, 0)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d", len(warnings))
	}
	if len(rules) != 0 {
		t.Fatalf("expected 0 rules, got %d", len(rules))
	}
}

func TestParseRedirectsMissingDestination(t *testing.T) {
	input := `/only-source
`
	rules, warnings := ParseRedirects(strings.NewReader(input), 0, 0, 0)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d", len(warnings))
	}
	if len(rules) != 0 {
		t.Fatalf("expected 0 rules, got %d", len(rules))
	}
}

func TestParseRedirectsTooManyFields(t *testing.T) {
	input := `/old /new 301 extra
`
	rules, warnings := ParseRedirects(strings.NewReader(input), 0, 0, 0)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d", len(warnings))
	}
	if len(rules) != 0 {
		t.Fatalf("expected 0 rules, got %d", len(rules))
	}
}

func TestParseRedirectsSourceMustStartWithSlash(t *testing.T) {
	input := `old /new 301
`
	rules, warnings := ParseRedirects(strings.NewReader(input), 0, 0, 0)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d", len(warnings))
	}
	if len(rules) != 0 {
		t.Fatalf("expected 0 rules, got %d", len(rules))
	}
}

func TestParseRedirectsDestinationMustBeValid(t *testing.T) {
	input := `/old relative-path 301
`
	rules, warnings := ParseRedirects(strings.NewReader(input), 0, 0, 0)
	if len(warnings) != 1 {
		t.Fatalf("expected 1 warning, got %d", len(warnings))
	}
	if len(rules) != 0 {
		t.Fatalf("expected 0 rules, got %d", len(rules))
	}
}

func TestParseRedirectsMaxRules(t *testing.T) {
	var sb strings.Builder
	for i := 0; i < 5; i++ {
		sb.WriteString("/path" + fmt.Sprint(i) + " /dest 301\n")
	}

	rules, warnings := ParseRedirects(strings.NewReader(sb.String()), 3, 0, 0)
	if len(rules) != 3 {
		t.Fatalf("expected 3 rules (limited), got %d", len(rules))
	}
	if len(warnings) == 0 {
		t.Fatal("expected warnings about exceeding max rules")
	}
}

func TestParseRedirectsMultipleWildcards(t *testing.T) {
	input := `/*/* /dest 301
`
	rules, warnings := ParseRedirects(strings.NewReader(input), 0, 0, 0)
	if len(rules) != 0 {
		t.Fatalf("expected 0 rules, got %d", len(rules))
	}
	if len(warnings) < 1 {
		t.Fatalf("expected at least 1 warning, got %d", len(warnings))
	}
}

func TestParseRedirectsAllStatusCodes(t *testing.T) {
	for _, code := range []int{301, 302, 303, 307, 308} {
		input := fmt.Sprintf("/old /new %d\n", code)
		rules, warnings := ParseRedirects(strings.NewReader(input), 0, 0, 0)
		if len(warnings) > 0 {
			t.Errorf("status %d: unexpected warnings: %v", code, warnings)
		}
		if len(rules) != 1 {
			t.Errorf("status %d: expected 1 rule, got %d", code, len(rules))
			continue
		}
		if rules[0].Status != code {
			t.Errorf("expected status %d, got %d", code, rules[0].Status)
		}
	}
}

func TestParseRedirectsEmptyFile(t *testing.T) {
	rules, warnings := ParseRedirects(strings.NewReader(""), 0, 0, 0)
	if len(warnings) > 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if len(rules) != 0 {
		t.Fatalf("expected 0 rules, got %d", len(rules))
	}
}

func TestParseRedirectsOnlyComments(t *testing.T) {
	input := `# Just comments
# Nothing else
`
	rules, warnings := ParseRedirects(strings.NewReader(input), 0, 0, 0)
	if len(warnings) > 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if len(rules) != 0 {
		t.Fatalf("expected 0 rules, got %d", len(rules))
	}
}
