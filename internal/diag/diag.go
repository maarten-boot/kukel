// Package diag defines the error type shared by the lexer, parser, checker
// and compiler: a message at a source position.
package diag

import (
	"fmt"

	"github.com/maarten-boot/min-kukel/internal/token"
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
