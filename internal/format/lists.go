package format

// Lists put one per line (TODO.md Q106): with Strict, the arguments of a
// call with more than 3 of them, and those of a call, the parameters of a
// definition or the elements of a vector or map literal on a line longer
// than Options.LineLength; with Vertical, every argument list of a call,
// parameter list of a function or member function definition, and vector
// and map literal with 2 or more items. Such a list gets a line
// break after its "(" and after each item's ",", a "," after its last item,
// and its ")" on a line of its own; layout then indents the items one level
// and lines the ")" up with the line the list started on.

import (
	"bytes"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/maarten-boot/kukel/internal/ast"
	"github.com/maarten-boot/kukel/internal/token"
)

// list is an argument or parameter list in the tokens: the indexes of its
// "(" and ")", and of the "," between its items (a "," after the last item
// is not one of them: trailing tells).
type list struct {
	lp, rp   int
	commas   []int
	trailing bool // a "," after the last item
	empty    bool // no items (perhaps comments)
	def      bool // the parameters of a definition
	literal  bool // the elements of a vector or map literal ("[" or "{")
}

func (l list) items() int {
	if l.empty {
		return 0
	}
	return len(l.commas) + 1
}

// lists finds the argument lists of the calls (f(...), p->m(...), new
// C(...)) and the parameter lists of the function and member function
// definitions of tree, by their "(" in toks.
func lists(tree *ast.File, toks []token.Token) []list {
	var lps []int
	defs, literals := map[int]bool{}, map[int]bool{}
	after := func(id *ast.Ident) int { // the "(" after a name
		i := tokenAt(toks, id.NamePos) + 1
		for i < len(toks) && toks[i].Kind == token.COMMENT {
			i++
		}
		return i
	}
	for _, d := range tree.Decls {
		if fd, ok := d.(*ast.FuncDecl); ok {
			i := after(fd.Name)
			lps, defs[i] = append(lps, i), true
		}
		ast.Inspect(d, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.CallExpr:
				lps = append(lps, tokenAt(toks, n.Lparen))
			case *ast.MethodCall:
				lps = append(lps, after(n.Name))
			case *ast.NewExpr:
				lps = append(lps, after(n.Class))
			case *ast.VectorLit:
				i := tokenAt(toks, n.Lbrack)
				lps, literals[i] = append(lps, i), true
			case *ast.MapLit:
				i := tokenAt(toks, n.Lbrace)
				lps, literals[i] = append(lps, i), true
			}
			return true
		})
	}
	var ls []list
	for _, lp := range lps {
		if lp >= len(toks) || toks[lp].Kind != token.LPAREN && !literals[lp] {
			continue // (not expected: the tree and the tokens agree)
		}
		l := list{lp: lp, def: defs[lp], literal: literals[lp]}
		depth := 0
	scan:
		for i := lp + 1; i < len(toks); i++ {
			switch toks[i].Kind {
			case token.LPAREN, token.LBRACK, token.LBRACE:
				depth++
			case token.RPAREN, token.RBRACK, token.RBRACE:
				if depth == 0 {
					l.rp = i
					break scan
				}
				depth--
			case token.COMMA:
				if depth == 0 {
					l.commas = append(l.commas, i)
				}
			}
		}
		if n := len(l.commas); n > 0 && code(toks, l.commas[n-1]+1, l.rp) == -1 {
			l.commas, l.trailing = l.commas[:n-1], true
		}
		l.empty = code(toks, l.lp+1, l.rp) == -1
		ls = append(ls, l)
	}
	sort.Slice(ls, func(i, j int) bool { return ls[i].lp < ls[j].lp })
	return ls
}

// code gives the index of the first token from i up to (not including) end
// that is not a comment, or -1.
func code(toks []token.Token, i, end int) int {
	for ; i < end; i++ {
		if toks[i].Kind != token.COMMENT {
			return i
		}
	}
	return -1
}

// byCount chooses the lists that Strict (more than 3 arguments of a call)
// and Vertical (2 or more items of a call, definition or vector or map
// literal) put one per line,
// and the lists around them that they are an item of (directly or inside
// other items, not inside a function literal's body: f(g(1, 2, 3, 4), x)
// puts g's arguments and f's one per line; a vector or map literal in
// between is a list itself, so it is split too and passes it on), so that
// a list is never half on one line and half on several.
func byCount(ls []list, toks []token.Token, vertical bool) []list {
	chosen := map[int]bool{}
	for _, l := range ls {
		n := l.items()
		if !l.def && !l.literal && n > 3 || vertical && n >= 2 {
			chosen[l.lp] = true
		}
	}
	for changed := true; changed; {
		changed = false
		for _, outer := range ls {
			if chosen[outer.lp] || outer.items() == 0 {
				continue
			}
			for _, inner := range ls {
				if chosen[inner.lp] && outer.lp < inner.lp && inner.rp < outer.rp && !braced(toks, outer.lp, inner.lp) {
					chosen[outer.lp], changed = true, true
					break
				}
			}
		}
	}
	var out []list
	for _, l := range ls {
		if chosen[l.lp] {
			out = append(out, l)
		}
	}
	return out
}

// braced reports whether a "{" opened after toks[from] is still open at
// toks[to]: to is inside a block or a map literal there. A map literal is
// a list of its own, so the lists around it are still reached through it.
func braced(toks []token.Token, from, to int) bool {
	depth := 0
	for i := from + 1; i < to; i++ {
		switch toks[i].Kind {
		case token.LBRACE:
			depth++
		case token.RBRACE:
			depth--
		}
	}
	return depth > 0
}

// split reports whether the list is one item per line already: its first
// item and its ")" start lines (each item after a ",": see breaks).
func split(l list, toks []token.Token) bool {
	return len(breaks(l, toks)) == 0 && l.trailing
}

// breaks gives the token indexes in front of which a line break goes, so
// that each item of l and its ")" start a line: the token after the "(",
// the token after each "," (a comment there goes to the next line with the
// item), and the ")". Where a line starts already, none.
func breaks(l list, toks []token.Token) []int {
	starts := []int{l.lp + 1}
	for _, c := range l.commas {
		starts = append(starts, c+1)
	}
	if starts[len(starts)-1] != l.rp {
		starts = append(starts, l.rp)
	}
	var at []int
	for _, i := range starts {
		if endLine(toks[i-1]) == toks[i].Pos.Line {
			at = append(at, i)
		}
	}
	return at
}

func endLine(t token.Token) int { return t.Pos.Line + strings.Count(t.Text, "\n") }

// breakLists gives src with the lists put one item per line: the line
// breaks of breaks, and a "," after the last item where there is none.
func breakLists(ls []list, toks []token.Token, src []byte) []byte {
	type insert struct {
		at   int
		text string
	}
	var ins []insert
	seen := map[int]bool{}
	for _, l := range ls {
		if seen[l.lp] || l.items() == 0 {
			continue
		}
		seen[l.lp] = true
		if !l.trailing {
			last := l.rp - 1
			for toks[last].Kind == token.COMMENT {
				last--
			}
			ins = append(ins, insert{toks[last].Pos.Offset + len(toks[last].Text), ","})
		}
		for _, i := range breaks(l, toks) {
			ins = append(ins, insert{toks[i].Pos.Offset, "\n"})
		}
	}
	sort.SliceStable(ins, func(i, j int) bool { return ins[i].at < ins[j].at })
	var b bytes.Buffer
	last := -1
	prev := 0
	for _, in := range ins {
		if in.text == "\n" && in.at == last {
			continue // two lists break at one place
		}
		b.Write(src[prev:in.at])
		b.WriteString(in.text)
		prev = in.at
		if in.text == "\n" {
			last = in.at
		}
	}
	b.Write(src[prev:])
	return b.Bytes()
}

// tooLong chooses, for each line of src (formatted) longer than max
// characters, the outermost list that starts on it and is not one item per
// line yet.
func tooLong(ls []list, toks []token.Token, src []byte, max int) []list {
	long := map[int]bool{}
	for n, line := range bytes.Split(src, []byte("\n")) {
		if utf8.RuneCount(line) > max {
			long[n+1] = true
		}
	}
	var chosen []list
	end := -1 // the ")" of the last list chosen: lists inside it wait for the next round
	for _, l := range ls {
		if l.lp < end || !long[toks[l.lp].Pos.Line] || l.items() == 0 || split(l, toks) {
			continue
		}
		chosen = append(chosen, l)
		end = l.rp
	}
	return chosen
}
