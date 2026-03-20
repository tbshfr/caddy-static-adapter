package staticadapter

import (
	"sort"
	"strings"
)

// compiledRule wraps a Rule with its precomputed path pattern and optional host.
type compiledRule struct {
	*Rule
	pathPattern string
	host        string // non-empty for absolute URL patterns

	hasSplat          bool
	placeholderTokens []string
	needsSplat        bool
	needsPlaceholders bool
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
}

// Compile takes parsed rules and compiles them into a Compiled structure
// for efficient matching.
func Compile(rules []*Rule) *Compiled {
	c := &Compiled{
		exactPaths: make(map[string][]indexedRule, len(rules)),
	}

	for i, rule := range rules {
		cr := &compiledRule{
			Rule:        rule,
			pathPattern: extractPath(rule.Pattern),
			host:        extractHost(rule.Pattern),
		}
		cr.hasSplat = strings.Contains(cr.pathPattern, "*")
		if strings.Contains(cr.pathPattern, ":") {
			for _, part := range strings.Split(cr.pathPattern, "/") {
				if strings.HasPrefix(part, ":") && len(part) > 1 {
					cr.placeholderTokens = append(cr.placeholderTokens, part)
				}
			}
			sort.Slice(cr.placeholderTokens, func(i, j int) bool {
				return len(cr.placeholderTokens[i]) > len(cr.placeholderTokens[j])
			})
		}
		if cr.hasSplat || len(cr.placeholderTokens) > 0 {
			for _, op := range cr.Ops {
				if !strings.Contains(op.Value, ":") {
					continue
				}
				if cr.hasSplat && strings.Contains(op.Value, ":splat") {
					cr.needsSplat = true
				}
				if len(cr.placeholderTokens) > 0 {
					for _, token := range cr.placeholderTokens {
						if strings.Contains(op.Value, token) {
							cr.needsPlaceholders = true
							break
						}
					}
				}
				if cr.needsSplat && cr.needsPlaceholders {
					break
				}
			}
		}
		ir := indexedRule{index: i, cr: cr}

		if hasWildcardOrPlaceholder(cr.pathPattern) {
			c.wildcards = append(c.wildcards, ir)
		} else {
			c.exactPaths[cr.pathPattern] = append(c.exactPaths[cr.pathPattern], ir)
		}
	}

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
			ops = append(ops, ir.cr.Ops...)
		} else {
			ir := c.wildcards[wi]
			wi++
			if ir.cr.host != "" && !matchHost(ir.cr.host, requestHost) {
				continue
			}
			if matchPattern(ir.cr.pathPattern, requestPath) {
				ops = append(ops, expandOps(ir.cr, requestPath)...)
			}
		}
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
		ops = append(ops, ir.cr.Ops...)
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
		if matchPattern(ir.cr.pathPattern, requestPath) {
			ops = append(ops, expandOps(ir.cr, requestPath)...)
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

// expandOps expands placeholder references in header operation values.
func expandOps(cr *compiledRule, path string) []HeaderOp {
	if !cr.needsSplat && !cr.needsPlaceholders {
		return cr.Ops
	}

	// Extract splat value if pattern has wildcard.
	var splatValue string
	if cr.needsSplat {
		idx := strings.Index(cr.pathPattern, "*")
		prefix := cr.pathPattern[:idx]
		suffix := cr.pathPattern[idx+1:]
		remaining := path[len(prefix):]
		if suffix != "" {
			remaining = strings.TrimSuffix(remaining, suffix)
		}
		splatValue = remaining
	}

	// Extract placeholder values.
	var placeholders map[string]string
	if cr.needsPlaceholders {
		placeholders, _ = extractPlaceholders(cr.pathPattern, path)
	}

	expanded := make([]HeaderOp, len(cr.Ops))
	for i, op := range cr.Ops {
		expanded[i] = op
		if strings.Contains(op.Value, ":") {
			val := op.Value
			if cr.needsSplat {
				val = strings.ReplaceAll(val, ":splat", splatValue)
			}
			for _, token := range cr.placeholderTokens {
				if !strings.Contains(val, token) {
					continue
				}
				if pval, ok := placeholders[token[1:]]; ok {
					val = strings.ReplaceAll(val, token, pval)
				}
			}
			expanded[i].Value = val
		}
	}

	return expanded
}
