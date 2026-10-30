package compiler

import (
	"fmt"

	"github.com/maarten-boot/kukel/internal/ast"
	"github.com/maarten-boot/kukel/internal/bytecode"
)

// funcLit compiles a function literal as a function of its own and returns
// its index in Program.Funcs. It shares the class and this of the function
// it is written in.
func (c *compiler) funcLit(e *ast.FuncLit) int {
	idx := len(c.prog.Funcs)
	if idx > maxIndex {
		c.errorf(e.Fn, "too many functions (more than %d)", maxIndex+1)
	}
	c.prog.Funcs = append(c.prog.Funcs, nil) // reserved; nested literals come after
	name := fmt.Sprintf("%s/fn@%d", c.fs.fn.FuncName, e.Fn.Line)
	c.prog.Funcs[idx] = c.function(name, e.Fn, e.Params, c.fs.class, c.fs.static, c.fs, e, func() {
		c.block(e.Body)
	})
	return idx
}

// upvalue resolves name as a variable of a function enclosing fs (a
// function literal) and returns its upvalue index in fs. Each function in
// between passes the cell on as an upvalue of its own.
func (c *compiler) upvalue(fs *funcState, name string) (int, bool) {
	if i, ok := fs.upvalues[name]; ok {
		return i, true
	}
	p := fs.parent
	if p == nil {
		return 0, false
	}
	var u bytecode.Upvalue
	if lv, ok := p.lookup(name); ok {
		if !lv.cell {
			c.errorf(fs.fn.Pos(), "internal error: captured variable %s is not in a cell", name)
		}
		u = bytecode.Upvalue{Name: name, FromLocal: true, Index: lv.slot}
	} else if i, ok := c.upvalue(p, name); ok {
		u = bytecode.Upvalue{Name: name, Index: i}
	} else {
		return 0, false
	}
	if len(fs.fn.Upvalues) > maxIndex {
		c.errorf(fs.fn.Pos(), "too many captured variables (more than %d)", maxIndex+1)
	}
	fs.fn.Upvalues = append(fs.fn.Upvalues, u)
	fs.upvalues[name] = len(fs.fn.Upvalues) - 1
	return len(fs.fn.Upvalues) - 1, true
}

// capturedNames returns the names used anywhere inside the function
// literals nested in tree (not counting tree itself). A local with such a
// name lives in a cell, because a literal may capture it. This is an
// over-approximation (a literal may mean another variable with the same
// name), which costs a cell but is always correct.
func capturedNames(tree ast.Node) map[string]bool {
	names := map[string]bool{}
	if tree == nil {
		return names
	}
	ast.Inspect(tree, func(n ast.Node) bool {
		fl, ok := n.(*ast.FuncLit)
		if !ok || n == tree {
			return true
		}
		ast.Inspect(fl, func(m ast.Node) bool {
			if id, ok := m.(*ast.Ident); ok {
				names[id.Name] = true
			}
			return true
		})
		return false
	})
	return names
}

// yieldIn returns the first yield of a function (a declaration or a
// function literal), not counting those of the function literals inside
// it, or nil if it has none.
func yieldIn(tree ast.Node) ast.Node {
	switch tree.(type) {
	case *ast.FuncDecl, *ast.FuncLit:
	default:
		return nil // the initializers, the REPL's input
	}
	var y ast.Node
	ast.Inspect(tree, func(n ast.Node) bool {
		if _, ok := n.(*ast.FuncLit); ok && n != tree || y != nil {
			return false
		}
		switch n.(type) {
		case *ast.YieldStmt, *ast.YieldExpr:
			y = n
		}
		return true
	})
	return y
}

// storeFresh stores the value on the stack into a declared variable,
// making a new cell for a captured one (a fresh variable each time).
func (c *compiler) storeFresh(lv localVar) {
	if lv.cell {
		c.emit(bytecode.OpNewCell, lv.slot)
	} else {
		c.emit(bytecode.OpStoreLocal, lv.slot)
	}
}
