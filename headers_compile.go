package staticadapter

import (
	"math"
	"slices"
	"sort"
)

// compiledRule wraps a Rule with its compiled path pattern and optional host.
type compiledRule struct {
	*Rule
	pattern *compiledPattern
	host    string // non-empty for absolute URL patterns

	ops compiledOps

	// dedupOps is Ops with repeated headers comma-joined, used in dedup
	// mode so that "last wins" only applies across rules, not within one.
	dedupOps compiledOps

	// specificity measures how specific the pattern is. Higher values
	// mean more specific. See computeSpecificity.
	specificity int
}

// compiledOps holds header operations with their values compiled as
// templates against the rule's pattern.
type compiledOps struct {
	ops     []HeaderOp
	values  []template // parallel to ops; nil when no value references the pattern
	dynamic bool
}

func compileOps(ops []HeaderOp, p *compiledPattern) compiledOps {
	c := compiledOps{ops: ops}
	for i, op := range ops {
		t := compileTemplate(op.Value, p)
		if !t.dynamic() {
			continue
		}
		if c.values == nil {
			c.values = make([]template, len(ops))
			for j := range ops {
				c.values[j] = template{raw: ops[j].Value}
			}
		}
		c.values[i] = t
		c.dynamic = true
	}
	return c
}

// indexedRule pairs a compiledRule with its original position in the file,
// enabling deterministic ordering when merging results from different buckets.
type indexedRule struct {
	index int
	cr    *compiledRule
}

// Compiled holds the compiled rules indexed for efficient matching.
// Exact-path rules are stored in a map for O(1) lookup; wildcard and
// placeholder rules are kept in a separate slice. Both preserve original
// definition order for deterministic, Cloudflare-compatible header application.
type Compiled struct {
	// exactPaths maps normalized path → indexed rules with that exact path.
	exactPaths map[string][]indexedRule
	// wildcards holds rules containing * or : patterns in definition order.
	wildcards []indexedRule
	// bySpecificity holds the wildcards sorted by specificity (least
	// specific first, file order for ties) for dedup matching.
	bySpecificity []indexedRule
}

// Compile takes parsed rules and compiles them into a Compiled structure
// for efficient matching.
func Compile(rules []*Rule) *Compiled {
	c := &Compiled{
		exactPaths: make(map[string][]indexedRule, len(rules)),
	}

	for i, rule := range rules {
		p := compilePattern(extractPath(rule.Pattern))
		ops := compileOps(rule.Ops, p)
		cr := &compiledRule{
			Rule:    rule,
			pattern: p,
			host:    extractHost(rule.Pattern),
			ops:     ops,
			// Merging compiled values rather than compiling merged ones
			// keeps a '?' in one value from making placeholders in the
			// values after it query-escaped.
			dedupOps: ops.mergeRepeated(),
		}
		// Compute specificity for dedup ordering.
		cr.specificity = computeSpecificity(p)

		ir := indexedRule{index: i, cr: cr}

		if p.exact() {
			c.exactPaths[p.raw] = append(c.exactPaths[p.raw], ir)
		} else {
			c.wildcards = append(c.wildcards, ir)
		}
	}

	c.bySpecificity = slices.Clone(c.wildcards)
	sort.SliceStable(c.bySpecificity, func(i, j int) bool {
		return c.bySpecificity[i].cr.specificity < c.bySpecificity[j].cr.specificity
	})

	return c
}

// MatchOrdered returns all header operations preserving original rule definition
// order across both exact and wildcard matches, matching Cloudflare semantics.
// The host parameter should be the request's Host header (without port).
// If host is empty, absolute URL patterns will not match.
func (c *Compiled) MatchOrdered(requestPath, requestHost string) []HeaderOp {
	requestPath = normalizePath(requestPath)

	exactRules := c.exactPaths[requestPath]

	// Fast path: no wildcards — only exact matches to check.
	if len(c.wildcards) == 0 {
		return matchExactOnly(exactRules, requestHost)
	}

	// Fast path: no exact matches — only scan wildcards.
	if len(exactRules) == 0 {
		return matchWildcardOnly(c.wildcards, requestPath, requestHost)
	}

	// Both exact and wildcard candidates: two-pointer merge by original
	// rule index preserves file-order semantics required by Cloudflare.
	var ops []HeaderOp
	ei, wi := 0, 0

	for ei < len(exactRules) || wi < len(c.wildcards) {
		useExact := ei < len(exactRules) &&
			(wi >= len(c.wildcards) || exactRules[ei].index < c.wildcards[wi].index)

		if useExact {
			ir := exactRules[ei]
			ei++
			if ir.cr.host != "" && !matchHost(ir.cr.host, requestHost) {
				continue
			}
			ops = append(ops, ir.cr.ops.ops...)
		} else {
			ir := c.wildcards[wi]
			wi++
			if ir.cr.host != "" && !matchHost(ir.cr.host, requestHost) {
				continue
			}
			if ir.cr.pattern.match(requestPath) {
				ops = append(ops, expandOps(ir.cr, ir.cr.ops, requestPath)...)
			}
		}
	}

	return ops
}

// MatchBySpecificity returns all header operations for matching rules, sorted
// by pattern specificity (least specific first). When used with dedup "last
// wins" semantics, the most specific matching rule's value takes precedence.
// Rules with equal specificity preserve their original file order.
func (c *Compiled) MatchBySpecificity(requestPath, requestHost string) []HeaderOp {
	requestPath = normalizePath(requestPath)

	// Wildcards are pre-sorted by specificity; exact matches are the most
	// specific and come last, in file order.
	var ops []HeaderOp
	for _, ir := range c.bySpecificity {
		if ir.cr.host != "" && !matchHost(ir.cr.host, requestHost) {
			continue
		}
		if ir.cr.pattern.match(requestPath) {
			ops = append(ops, expandOps(ir.cr, ir.cr.dedupOps, requestPath)...)
		}
	}
	for _, ir := range c.exactPaths[requestPath] {
		if ir.cr.host != "" && !matchHost(ir.cr.host, requestHost) {
			continue
		}
		ops = append(ops, ir.cr.dedupOps.ops...)
	}
	return ops
}

// matchExactOnly returns ops from exact-match rules, checking host constraints.
func matchExactOnly(rules []indexedRule, requestHost string) []HeaderOp {
	var ops []HeaderOp
	for _, ir := range rules {
		if ir.cr.host != "" && !matchHost(ir.cr.host, requestHost) {
			continue
		}
		ops = append(ops, ir.cr.ops.ops...)
	}
	return ops
}

// matchWildcardOnly returns ops from wildcard rules, checking host and pattern.
func matchWildcardOnly(wildcards []indexedRule, requestPath, requestHost string) []HeaderOp {
	var ops []HeaderOp
	for _, ir := range wildcards {
		if ir.cr.host != "" && !matchHost(ir.cr.host, requestHost) {
			continue
		}
		if ir.cr.pattern.match(requestPath) {
			ops = append(ops, expandOps(ir.cr, ir.cr.ops, requestPath)...)
		}
	}
	return ops
}

// totalRules returns the total number of compiled rules across all buckets.
func (c *Compiled) totalRules() int {
	total := len(c.wildcards)
	for _, rules := range c.exactPaths {
		total += len(rules)
	}
	return total
}

// computeSpecificity returns the specificity score for a compiled pattern.
// Exact matches get the highest score (math.MaxInt). All other patterns are
// scored on one scale, by their number of literal characters (everything
// except placeholders and the '*'), so that e.g. /blog/:slug beats /b*.
// At equal length a pattern without a splat wins, since a placeholder
// matches a single segment while '*' matches anything.
func computeSpecificity(p *compiledPattern) int {
	if p.exact() {
		// Exact match — highest specificity.
		return math.MaxInt
	}

	score := p.literalLen * 2
	if !p.hasSplat {
		score++
	}
	return score
}

// expandOps expands placeholder references in the values of ops, which
// must be cr.ops or cr.dedupOps.
func expandOps(cr *compiledRule, ops compiledOps, path string) []HeaderOp {
	if !ops.dynamic {
		return ops.ops
	}

	values, splat, _ := cr.pattern.capture(path)
	expanded := make([]HeaderOp, len(ops.ops))
	for i, op := range ops.ops {
		expanded[i] = op
		expanded[i].Value = ops.values[i].expand(values, splat)
	}
	return expanded
}

// mergeRepeatedHeaders comma-joins repeated sets of the same header within
// one rule. A remove resets the header, so a set after it starts a new value.
// Headers that must not be comma-joined keep one op per value, with every
// value after the first turned into an append.
func mergeRepeatedHeaders(ops []HeaderOp) []HeaderOp {
	return compiledOps{ops: ops}.mergeRepeated().ops
}

// mergeRepeated is mergeRepeatedHeaders for compiled ops, joining the
// compiled values along with the raw ones.
func (c compiledOps) mergeRepeated() compiledOps {
	merged := compiledOps{ops: make([]HeaderOp, 0, len(c.ops)), dynamic: c.dynamic}
	if c.values != nil {
		merged.values = make([]template, 0, len(c.ops))
	}
	add := func(op HeaderOp, i int) {
		merged.ops = append(merged.ops, op)
		if c.values != nil {
			merged.values = append(merged.values, c.values[i])
		}
	}

	current := make(map[string]int) // header name → index of its set in merged
	for i, op := range c.ops {
		if op.Mode == OpRemove {
			delete(current, op.Name)
			add(op, i)
			continue
		}
		if j, ok := current[op.Name]; ok {
			if noCommaJoin(op.Name) {
				op.Mode = opAppend
				add(op, i)
				continue
			}
			merged.ops[j].Value += ", " + op.Value
			if c.values != nil {
				merged.values[j] = merged.values[j].join(", ", c.values[i])
			}
			continue
		}
		current[op.Name] = len(merged.ops)
		add(op, i)
	}
	return merged
}
