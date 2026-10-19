package staticadapter

import (
	"fmt"
	"strings"
	"testing"
)

func benchRedirects(b *testing.B, n int) *CompiledRedirects {
	b.Helper()
	var sb strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&sb, "/cat%d/:a/:b /new%d/:a/:b 301\n", i, i)
	}
	sb.WriteString("/blog/* /news/:splat 301\n")
	parsed, warnings := ParseRedirects(strings.NewReader(sb.String()), n+1, 0, 0)
	if len(warnings) != 0 {
		b.Fatal(warnings)
	}
	rules := make([]*compiledRedirect, len(parsed))
	for i, r := range parsed {
		rules[i] = newCompiledRedirect(r)
	}
	return compileRedirects(rules)
}

func BenchmarkRedirectsNoMatch500(b *testing.B) {
	c := benchRedirects(b, 500)
	b.ReportAllocs()
	for b.Loop() {
		if c.MatchFirst("/nomatch/x/y") != nil {
			b.Fatal("unexpected match")
		}
	}
}

func BenchmarkRedirectsMatchExpand500(b *testing.B) {
	c := benchRedirects(b, 500)
	b.ReportAllocs()
	for b.Loop() {
		match := c.MatchFirst("/cat250/foo/bar")
		if match == nil || match.expand("/cat250/foo/bar") != "/new250/foo/bar" {
			b.Fatal("unexpected result")
		}
	}
}

const benchHeaders = `/*
  X-Frame-Options: DENY
  X-Content-Type-Options: nosniff
/_nuxt/*
  Cache-Control: public, max-age=31536000, immutable
/_nuxt/builds/*
  Cache-Control: public, max-age=1, immutable
/blog/:slug
  X-Slug: :slug
/about
  X-Robots-Tag: index
`

func BenchmarkHeadersMatch(b *testing.B) {
	rules, _ := Parse(strings.NewReader(benchHeaders), 0, 0, 0)
	c := Compile(rules)
	b.ReportAllocs()
	for b.Loop() {
		c.MatchOrdered("/blog/hello", "")
		c.MatchOrdered("/_nuxt/builds/latest.json", "")
	}
}

func BenchmarkHeadersMatchDedup(b *testing.B) {
	rules, _ := Parse(strings.NewReader(benchHeaders), 0, 0, 0)
	c := Compile(rules)
	b.ReportAllocs()
	for b.Loop() {
		c.MatchBySpecificity("/blog/hello", "")
		c.MatchBySpecificity("/_nuxt/builds/latest.json", "")
	}
}

// All rules start with a placeholder, so the first-segment index can't
// narrow them down and every rule is checked.
func BenchmarkRedirectsNoMatch500Unindexed(b *testing.B) {
	var sb strings.Builder
	for i := 0; i < 500; i++ {
		fmt.Fprintf(&sb, "/:lang/cat%d/:b /new%d/:lang/:b 301\n", i, i)
	}
	parsed, _ := ParseRedirects(strings.NewReader(sb.String()), 0, 0, 0)
	rules := make([]*compiledRedirect, len(parsed))
	for i, r := range parsed {
		rules[i] = newCompiledRedirect(r)
	}
	c := compileRedirects(rules)
	b.ReportAllocs()
	for b.Loop() {
		if c.MatchFirst("/en/nomatch/y") != nil {
			b.Fatal("unexpected match")
		}
	}
}
