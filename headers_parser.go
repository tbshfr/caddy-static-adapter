package staticadapter

import (
	"bufio"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// HeaderMaxLineLength is the maximum number of characters allowed per line
// in a _headers file, matching Cloudflare's 2,000 character limit.
const HeaderMaxLineLength = 2000

// HeaderMaxRules is the default maximum number of header rules,
// matching Cloudflare's 100 rule limit.
const HeaderMaxRules = 100

// OpMode describes how a header operation is applied.
type OpMode int

const (
	// OpSet sets (appends via comma-join if duplicate) a header value.
	OpSet OpMode = iota
	// OpRemove removes a header.
	OpRemove
)

// HeaderOp represents a single header operation parsed from a _headers file.
type HeaderOp struct {
	Name  string
	Value string
	Mode  OpMode
}

// Rule represents a parsed URL pattern and its associated header operations.
type Rule struct {
	Pattern string
	Ops     []HeaderOp
	Line    int // line number where the pattern was defined
}

// Parse reads a _headers file from the given reader and returns the parsed rules.
// It enforces the given limits. If maxRules, maxFileSize, or maxLineLength are <= 0, the defaults are used.
func Parse(r io.Reader, maxRules, maxFileSize, maxLineLength int) ([]*Rule, []error) {
	if maxRules <= 0 {
		maxRules = HeaderMaxRules
	}
	if maxFileSize <= 0 {
		maxFileSize = MaxFileSize
	}
	if maxLineLength <= 0 {
		maxLineLength = HeaderMaxLineLength
	}

	lr := io.LimitReader(r, int64(maxFileSize)+1)
	scanner := bufio.NewScanner(lr)
	// Set a buffer large enough for max line length.
	scanner.Buffer(make([]byte, 0, maxLineLength+256), maxLineLength+256)

	var rules []*Rule
	var warnings []error
	var current *Rule
	lineNum := 0
	totalBytes := 0

	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		totalBytes += len(line) + 1 // +1 for newline

		if totalBytes > maxFileSize {
			warnings = append(warnings, &ParseError{Line: lineNum, Message: fmt.Sprintf("file exceeds maximum size of %d bytes", maxFileSize)})
			break
		}

		if len(line) > maxLineLength {
			warnings = append(warnings, &ParseError{Line: lineNum, Message: fmt.Sprintf("line exceeds maximum length of %d characters", maxLineLength)})
			continue
		}

		// Remove inline comments and check for comment/blank lines.
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		// Determine if this is a header line (indented) or a URL pattern line.
		isIndented := len(line) > 0 && (line[0] == ' ' || line[0] == '\t')

		if isIndented {
			if current == nil {
				warnings = append(warnings, &ParseError{Line: lineNum, Message: "header line without preceding URL pattern"})
				continue
			}

			op, err := parseHeaderLine(trimmed, lineNum)
			if err != nil {
				warnings = append(warnings, err)
				continue
			}
			current.Ops = append(current.Ops, op)
		} else {
			// This is a URL pattern line.
			pattern := trimmed

			if err := validatePattern(pattern, lineNum); err != nil {
				warnings = append(warnings, err)
				continue
			}

			if len(rules) >= maxRules {
				warnings = append(warnings, &ParseError{Line: lineNum, Message: fmt.Sprintf("exceeds maximum of %d rules", maxRules)})
				break
			}

			current = &Rule{
				Pattern: pattern,
				Line:    lineNum,
			}
			rules = append(rules, current)
		}
	}

	if err := scanner.Err(); err != nil {
		warnings = append(warnings, fmt.Errorf("scanner error: %w", err))
	}

	return rules, warnings
}

// parseHeaderLine parses a single header line like "Header-Name: value" or "! Header-Name".
func parseHeaderLine(line string, lineNum int) (HeaderOp, error) {
	// Check for remove syntax: "! Header-Name"
	if strings.HasPrefix(line, "! ") || strings.HasPrefix(line, "!") {
		name := strings.TrimSpace(strings.TrimPrefix(line, "!"))
		name = strings.TrimSpace(name)
		if name == "" {
			return HeaderOp{}, &ParseError{Line: lineNum, Message: "empty header name in remove directive"}
		}
		if err := validateHeaderName(name, lineNum); err != nil {
			return HeaderOp{}, err
		}
		return HeaderOp{
			Name: http.CanonicalHeaderKey(name),
			Mode: OpRemove,
		}, nil
	}

	// Normal header: "Name: value"
	colonIdx := strings.IndexByte(line, ':')
	if colonIdx < 1 {
		return HeaderOp{}, &ParseError{Line: lineNum, Message: fmt.Sprintf("invalid header line: %q (missing colon)", line)}
	}

	name := strings.TrimSpace(line[:colonIdx])
	value := strings.TrimSpace(line[colonIdx+1:])

	if name == "" {
		return HeaderOp{}, &ParseError{Line: lineNum, Message: "empty header name"}
	}

	if err := validateHeaderName(name, lineNum); err != nil {
		return HeaderOp{}, err
	}

	if err := validateHeaderValue(value, lineNum); err != nil {
		return HeaderOp{}, err
	}

	return HeaderOp{
		Name:  http.CanonicalHeaderKey(name),
		Value: value,
		Mode:  OpSet,
	}, nil
}

// validatePattern checks that a URL pattern is valid.
func validatePattern(pattern string, lineNum int) error {
	if pattern == "" {
		return &ParseError{Line: lineNum, Message: "empty URL pattern"}
	}

	// Absolute URL support: must start with https://
	if strings.Contains(pattern, "://") {
		if !strings.HasPrefix(pattern, "https://") {
			return &ParseError{Line: lineNum, Message: "absolute URLs must start with https://"}
		}
	}

	// Only one splat (*) allowed.
	if strings.Count(pattern, "*") > 1 {
		return &ParseError{Line: lineNum, Message: "only one wildcard (*) allowed per pattern"}
	}

	// Check for CRLF in pattern.
	if strings.ContainsAny(pattern, "\r\n") {
		return &ParseError{Line: lineNum, Message: "pattern contains invalid characters"}
	}

	return nil
}

// validateHeaderName checks that a header name is valid per HTTP spec.
func validateHeaderName(name string, lineNum int) error {
	if name == "" {
		return &ParseError{Line: lineNum, Message: "empty header name"}
	}

	for _, c := range name {
		if !isTokenChar(c) {
			return &ParseError{Line: lineNum, Message: fmt.Sprintf("invalid character %q in header name %q", c, name)}
		}
	}

	return nil
}

// validateHeaderValue checks that a header value doesn't contain CRLF (header injection).
func validateHeaderValue(value string, lineNum int) error {
	if strings.ContainsAny(value, "\r\n") {
		return &ParseError{Line: lineNum, Message: "header value contains CR/LF (potential header injection)"}
	}
	// Check for null bytes.
	if strings.ContainsRune(value, 0) {
		return &ParseError{Line: lineNum, Message: "header value contains null byte"}
	}
	return nil
}

// isTokenChar returns whether r is a valid HTTP token character per RFC 7230.
func isTokenChar(r rune) bool {
	// token = 1*tchar
	// tchar = "!" / "#" / "$" / "%" / "&" / "'" / "*" / "+" / "-" / "." /
	//         "^" / "_" / "`" / "|" / "~" / DIGIT / ALPHA
	// ALPHA and DIGIT are US-ASCII only per RFC 7230.
	if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
		return true
	}
	switch r {
	case '!', '#', '$', '%', '&', '\'', '*', '+', '-', '.', '^', '_', '`', '|', '~':
		return true
	}
	return false
}
