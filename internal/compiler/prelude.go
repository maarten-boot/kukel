package compiler

import (
	"kukel/internal/ast"
	"kukel/internal/parser"
)

// PreludeName is the file name of the prelude in positions and traces.
const PreludeName = "<prelude>"

// prelude is Kukel code compiled in front of every program. Its names are
// reserved like those of the built-ins.
//
// Error is the class of the values runtime errors throw (Q13); programs can
// throw it too, and derive their own error classes from it. When an Error
// is thrown the first time, the VM fills in trace (a vector of strings,
// innermost call first) and, if kind is nil, the name of its class.
const prelude = `
class Error {
    message, kind, trace;
    Error(message, kind);
}

Error::Error(message = nil, kind = nil) {
    this->message = message;
    this->kind = kind;
}
`

func preludeFile() *ast.File {
	f, err := parser.ParseFile(PreludeName, []byte(prelude))
	if err != nil {
		panic("kukel prelude: " + err.Error())
	}
	return f
}
