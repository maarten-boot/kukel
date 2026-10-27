package compiler

import (
	"github.com/maarten-boot/kukel/internal/ast"
	"github.com/maarten-boot/kukel/internal/bytecode"
	"github.com/maarten-boot/kukel/internal/token"
)

// tryCtx is an enclosing try block, catch block or finally block. A break,
// continue or return that leaves it first removes its handlers and runs its
// finally block.
type tryCtx struct {
	handlers  int        // handlers registered while inside it
	finally   *ast.Block // its finally block, to run when leaving, or nil
	inFinally bool       // this is a finally block itself: no jumping out
	targets   int        // len(funcState.targets) when it began

	// A for-each loop: leaving it closes the generator it may be walking
	// (ITER_CLOSE of the iterator in slot).
	iter bool
	slot int
}

func (c *compiler) pushTry(t *tryCtx) {
	t.targets = len(c.fs.targets)
	c.fs.tries = append(c.fs.tries, t)
}

func (c *compiler) popTry() { c.fs.tries = c.fs.tries[:len(c.fs.tries)-1] }

// tryStmt compiles try/catch/finally:
//
//	TRY_PUSH finally -> F          (with a finally block)
//	TRY_PUSH catch -> C            (with a catch block)
//	body
//	TRY_POP; JUMP after_catch      (with a catch block)
//	C: STORE e; catch body         (the handler is gone here)
//	after_catch: TRY_POP           (with a finally block)
//	finally body; JUMP end
//	F: STORE hidden; finally body; LOAD hidden; RETHROW
//	end:
func (c *compiler) tryStmt(s *ast.TryStmt) {
	if s.Finally == nil {
		c.tryCatch(s)
		return
	}
	h := c.emit(bytecode.OpTryPush, 1, 0)
	c.pushTry(&tryCtx{handlers: 1, finally: s.Finally})
	c.tryCatch(s)
	c.popTry()
	c.emit(bytecode.OpTryPop)
	c.finallyBlock(s.Finally)
	end := c.jump(bytecode.OpJump)

	// The exception path: the handler pushes the pending exception.
	c.patch([]int{h + 2}, c.here())
	c.handlerEntry()
	c.pushScope()
	pending := c.declare(&ast.Ident{NamePos: s.Finally.Lbrace, Name: "(exception)"}, "hidden").slot
	c.emit(bytecode.OpStoreLocal, pending)
	c.finallyBlock(s.Finally)
	c.emit(bytecode.OpLoadLocal, pending)
	c.emit(bytecode.OpRethrow)
	c.popScope()
	c.patch([]int{end}, c.here())
}

// tryCatch compiles the try block and, if there is one, the catch block.
func (c *compiler) tryCatch(s *ast.TryStmt) {
	if s.Catch == nil {
		c.block(s.Body)
		return
	}
	h := c.emit(bytecode.OpTryPush, 0, 0)
	c.pushTry(&tryCtx{handlers: 1})
	c.block(s.Body)
	c.popTry()
	c.emit(bytecode.OpTryPop)
	skip := c.jump(bytecode.OpJump)

	// The catch block: the handler pushes the thrown value. The catch
	// variable is a local of the catch block itself.
	c.patch([]int{h + 2}, c.here())
	c.handlerEntry()
	c.mark(s.CatchVar.NamePos)
	c.pushScope()
	c.declareInit(s.CatchVar)
	c.pushTry(&tryCtx{}) // marks the catch block (for case labels); no handler
	for _, st := range s.Catch.Stmts {
		if d, ok := st.(*ast.LocalDecl); ok {
			c.local(d)
		} else {
			c.stmt(st)
		}
	}
	c.popTry()
	c.popScope()
	c.patch([]int{skip}, c.here())
}

// handlerEntry notes that a handler starts with one value on the stack.
func (c *compiler) handlerEntry() {
	c.fs.depth = 1
	if c.fs.maxDepth < 1 {
		c.fs.maxDepth = 1
	}
}

// finallyBlock compiles a copy of a finally block.
func (c *compiler) finallyBlock(b *ast.Block) {
	c.pushTry(&tryCtx{inFinally: true})
	c.block(b)
	c.popTry()
}

// leaveTries emits what a jump to the target with index ti (-1 for a
// return) needs before it leaves try, catch and finally blocks: remove
// their handlers and run their finally blocks, innermost first.
func (c *compiler) leaveTries(ti int, pos token.Pos, what string) {
	tries := c.fs.tries
	for i := len(tries) - 1; i >= 0 && tries[i].targets > ti; i-- {
		if tries[i].inFinally {
			c.errorf(pos, "%s out of a finally block is not allowed", what)
		}
	}
	for i := len(tries) - 1; i >= 0 && tries[i].targets > ti; i-- {
		t := tries[i]
		for n := 0; n < t.handlers; n++ {
			c.emit(bytecode.OpTryPop)
		}
		if t.iter {
			c.emit(bytecode.OpIterClose, t.slot)
		}
		if t.finally != nil {
			// The finally block runs in the context outside this try.
			saved := c.fs.tries
			c.fs.tries = tries[:i:i] // capped: appending must not overwrite tries[i]
			c.finallyBlock(t.finally)
			c.fs.tries = saved
		}
	}
}

func (c *compiler) returnStmt(s *ast.ReturnStmt) {
	if c.fs.gen && s.Value != nil {
		c.errorf(s.Value.Pos(), "a generator cannot return a value (return; ends it, yield gives a value)")
	}
	if len(c.fs.tries) == 0 {
		if s.Value != nil {
			c.expr(s.Value)
			c.emit(bytecode.OpReturn)
		} else {
			c.emit(bytecode.OpReturnNil)
		}
		return
	}
	// Leaving try blocks: compute the value first, keep it in a hidden slot
	// while the finally blocks run.
	if s.Value == nil {
		c.leaveTries(-1, s.Return, "return")
		c.emit(bytecode.OpReturnNil)
		return
	}
	c.expr(s.Value)
	tmp := c.tempSlot()
	c.emit(bytecode.OpStoreLocal, tmp)
	c.leaveTries(-1, s.Return, "return")
	c.emit(bytecode.OpLoadLocal, tmp)
	c.freeTempSlot()
	c.emit(bytecode.OpReturn)
}

// checkLabelTry rejects a case or default label inside a try, catch or
// finally block within its switch body (Q9): the jump would enter it.
func (c *compiler) checkLabelTry(sw *switchState, pos token.Pos, what string) {
	for _, t := range c.fs.tries {
		if t.targets > sw.target && !t.iter {
			c.errorf(pos, "a %s label may not be inside a try, catch, finally or with block of its switch", what)
		}
	}
}

// withStmt compiles "with (local a = e1, b = e2) body" as nested try blocks:
// each variable is declared, then a try covers the rest; leaving it (at the
// end, by return, break or continue) runs a->close() if a is not nil. On
// the exception path the close runs inside a catch that ignores its
// failure, so that the body's exception is the one that goes on.
func (c *compiler) withStmt(s *ast.WithStmt) {
	c.pushScope()
	c.withVar(s, 0)
	c.popScope()
}

func (c *compiler) withVar(s *ast.WithStmt, i int) {
	if i == len(s.Vars) {
		c.stmt(s.Body)
		return
	}
	v := s.Vars[i]
	c.local(&ast.LocalDecl{Local: v.Pos(), Vars: []*ast.VarSpec{v}})
	normal, failing := closeBlocks(v.Name, s.With)
	h := c.emit(bytecode.OpTryPush, 1, 0)
	c.pushTry(&tryCtx{handlers: 1, finally: normal})
	c.withVar(s, i+1)
	c.popTry()
	c.emit(bytecode.OpTryPop)
	c.finallyBlock(normal)
	end := c.jump(bytecode.OpJump)

	c.patch([]int{h + 2}, c.here())
	c.handlerEntry()
	c.pushScope()
	pending := c.declare(&ast.Ident{NamePos: s.With, Name: "(exception)"}, "hidden").slot
	c.emit(bytecode.OpStoreLocal, pending)
	c.finallyBlock(failing)
	c.emit(bytecode.OpLoadLocal, pending)
	c.emit(bytecode.OpRethrow)
	c.popScope()
	c.patch([]int{end}, c.here())
}

// closeBlocks makes the code that closes the value of name:
//
//	normal:  if (name != nil) name->close();
//	failing: if (name != nil) try { name->close(); } catch ((ignored)) { }
func closeBlocks(name *ast.Ident, pos token.Pos) (normal, failing *ast.Block) {
	id := func() *ast.Ident { return &ast.Ident{NamePos: name.NamePos, Name: name.Name} }
	call := func() ast.Stmt {
		return &ast.ExprStmt{X: &ast.MethodCall{X: id(), Name: &ast.Ident{NamePos: pos, Name: "close"}}}
	}
	notNil := func() ast.Expr {
		return &ast.BinaryExpr{X: id(), OpPos: pos, Op: token.NE, Y: &ast.NilLit{ValuePos: pos}}
	}
	normal = &ast.Block{Lbrace: pos, Stmts: []ast.Stmt{&ast.IfStmt{If: pos, Cond: notNil(), Then: call()}}}
	guarded := &ast.TryStmt{Try: pos, Body: &ast.Block{Lbrace: pos, Stmts: []ast.Stmt{call()}},
		CatchVar: &ast.Ident{NamePos: pos, Name: "(ignored)"}, Catch: &ast.Block{Lbrace: pos}}
	failing = &ast.Block{Lbrace: pos, Stmts: []ast.Stmt{&ast.IfStmt{If: pos, Cond: notNil(), Then: guarded}}}
	return normal, failing
}
