package staticadapter

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// RedirectMaxRules is the default maximum number of redirect rules.
// Cloudflare allows 2000 static + 100 dynamic; we use 2000 as the overall default.
const RedirectMaxRules = 2000

// RedirectMaxLineLength is the default maximum line length for _redirects.
// Cloudflare uses 1000 characters.
const RedirectMaxLineLength = 1000

// RedirectRule represents a single parsed redirect rule.
type RedirectRule struct {
	From   string
	To     string
	Status int // HTTP status code (301, 302, 303, 307, 308)
	Line   int // line number where the rule was defined
}

// defaultRedirectStatus is the default status code when none is specified,
// matching Cloudflare Pages behavior.
const defaultRedirectStatus = 302

// validRedirectStatus reports whether the given status code is a valid
// redirect status code per Cloudflare Pages spec.
func validRedirectStatus(code int) bool {
	switch code {
	case 301, 302, 303, 307, 308:
		return true
	}
	return false
}

// ParseRedirects reads a _redirects file from the given reader and returns
// the parsed redirect rules. It enforces the given limits. If maxRules,
// maxFileSize, or maxLineLength are <= 0, the defaults are used.
func ParseRedirects(r io.Reader, maxRules, maxFileSize, maxLineLength int) ([]*RedirectRule, []error) {
	if maxRules <= 0 {
		maxRules = RedirectMaxRules
	}
	if maxFileSize <= 0 {
		maxFileSize = MaxFileSize
	}
	if maxLineLength <= 0 {
		maxLineLength = RedirectMaxLineLength
	}

	lr := io.LimitReader(r, int64(maxFileSize)+1)
	scanner := bufio.NewScanner(lr)
	scanner.Buffer(make([]byte, 0, maxLineLength+256), maxLineLength+256)

	var rules []*RedirectRule
	var warnings []error
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

		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		rule, err := parseRedirectLine(trimmed, lineNum)
		if err != nil {
			warnings = append(warnings, err)
			continue
		}

		if len(rules) >= maxRules {
			warnings = append(warnings, &ParseError{Line: lineNum, Message: fmt.Sprintf("exceeds maximum of %d redirect rules", maxRules)})
			break
		}

		rules = append(rules, rule)
	}

	if err := scanner.Err(); err != nil {
		warnings = append(warnings, fmt.Errorf("scanner error: %w", err))
	}

	return rules, warnings
}

// parseRedirectLine parses a single line of the _redirects file.
// Format: /from /to [status]
func parseRedirectLine(line string, lineNum int) (*RedirectRule, error) {
	fields := strings.Fields(line)
	if len(fields) < 2 {
		return nil, &ParseError{Line: lineNum, Message: "redirect rule requires at least a source and destination path"}
	}
	if len(fields) > 3 {
		return nil, &ParseError{Line: lineNum, Message: "redirect rule has too many fields (expected: /from /to [status])"}
	}

	from := fields[0]
	to := fields[1]
	status := defaultRedirectStatus

	// Validate from path.
	if err := validateRedirectFrom(from, lineNum); err != nil {
		return nil, err
	}

	// Validate to path.
	if err := validateRedirectTo(to, lineNum); err != nil {
		return nil, err
	}

	// Parse optional status code.
	if len(fields) == 3 {
		code, err := strconv.Atoi(fields[2])
		if err != nil {
			return nil, &ParseError{Line: lineNum, Message: fmt.Sprintf("invalid status code %q", fields[2])}
		}
		if !validRedirectStatus(code) {
			return nil, &ParseError{Line: lineNum, Message: fmt.Sprintf("unsupported redirect status code %d (must be 301, 302, 303, 307, or 308)", code)}
		}
		status = code
	}

	return &RedirectRule{
		From:   from,
		To:     to,
		Status: status,
		Line:   lineNum,
	}, nil
}

// validateRedirectFrom checks that a redirect source path is valid.
func validateRedirectFrom(from string, lineNum int) error {
	if from == "" {
		return &ParseError{Line: lineNum, Message: "empty redirect source path"}
	}

	// Must start with / (relative paths only for source).
	if !strings.HasPrefix(from, "/") {
		return &ParseError{Line: lineNum, Message: "redirect source path must start with /"}
	}

	// Check for CRLF.
	if strings.ContainsAny(from, "\r\n") {
		return &ParseError{Line: lineNum, Message: "redirect source path contains invalid characters"}
	}

	// Only one splat (*) allowed.
	if strings.Count(from, "*") > 1 {
		return &ParseError{Line: lineNum, Message: "only one wildcard (*) allowed per redirect source path"}
	}

	return nil
}

// validateRedirectTo checks that a redirect destination is valid.
func validateRedirectTo(to string, lineNum int) error {
	if to == "" {
		return &ParseError{Line: lineNum, Message: "empty redirect destination"}
	}

	// Must start with / or be an absolute URL.
	if !strings.HasPrefix(to, "/") && !strings.HasPrefix(to, "https://") && !strings.HasPrefix(to, "http://") {
		return &ParseError{Line: lineNum, Message: "redirect destination must start with / or be an absolute URL"}
	}

	// Check for CRLF.
	if strings.ContainsAny(to, "\r\n") {
		return &ParseError{Line: lineNum, Message: "redirect destination contains invalid characters"}
	}

	return nil
}
