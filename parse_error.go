package staticadapter

import "fmt"

// MaxFileSize is the default maximum file size in bytes (1 MB).
// Shared by both _headers and _redirects parsers.
const MaxFileSize = 1 << 20

// ParseError represents a parse error with line number context.
type ParseError struct {
	Line    int
	Message string
}

func (e *ParseError) Error() string {
	return fmt.Sprintf("line %d: %s", e.Line, e.Message)
}
