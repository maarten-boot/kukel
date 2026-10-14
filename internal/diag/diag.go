// Package diag defines the error type shared by the lexer, parser and
// compiler: a message at a source position.
package diag

import (
	"fmt"
	"strings"

	"github.com/maarten-boot/kukel/internal/token"
)

// Error is a compile-time error at a source position.
type Error struct {
	Pos token.Pos
	Msg string
}

func (e *Error) Error() string { return e.Pos.String() + ": " + e.Msg }

// Errorf returns an *Error at p.
func Errorf(p token.Pos, format string, args ...any) *Error {
	return &Error{Pos: p, Msg: fmt.Sprintf(format, args...)}
}

// MaxErrors is how many errors a List shows; more are counted.
const MaxErrors = 10

// List is several compile errors, in the order of the source.
type List []*Error

// Error gives one line per error (at most MaxErrors, then a count of the
// others).
func (l List) Error() string {
	var b strings.Builder
	for i, e := range l {
		if i == MaxErrors {
			fmt.Fprintf(&b, "\n(and %d more errors)", len(l)-MaxErrors)
			break
		}
		if i > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(e.Error())
	}
	return b.String()
}

// Errors gives the errors in err: the elements of a List, a single *Error,
// or nil for any other error.
func Errors(err error) []*Error {
	switch e := err.(type) {
	case List:
		return e
	case *Error:
		return []*Error{e}
	}
	return nil
}

// Join gives nil, the one error, or a List of several.
func Join(errs []*Error) error {
	switch len(errs) {
	case 0:
		return nil
	case 1:
		return errs[0]
	}
	return List(errs)
}
