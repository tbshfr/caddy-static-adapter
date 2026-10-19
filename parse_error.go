package staticadapter

import "fmt"

// MaxFileSize is the default maximum file size in bytes (1 MB).
// Shared by both _headers and _redirects parsers.
const MaxFileSize = 1 << 20

// ParseError represents a parse error with line number context.
type ParseError struct {
	Line      int
	Message   string
	NonStrict bool // when true, this warning never causes strict-mode failure
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("line %d: %s", e.Line, e.Message)
}

// firstStrictError returns the first warning that fails strict mode, or nil.
// Warnings marked NonStrict (e.g. unsupported 404/410 redirect rules) never do.
func firstStrictError(warnings []error) error {
	for _, w := range warnings {
		if pe, ok := w.(*ParseError); ok && pe.NonStrict {
			continue
		}
		return w
	}
	return nil
}
