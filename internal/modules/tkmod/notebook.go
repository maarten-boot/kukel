//go:build tk

package tkmod

import (
	tk "modernc.org/tk9.0"

	"github.com/maarten-boot/kukel/internal/native"
	"github.com/maarten-boot/kukel/internal/value"
)

// Notebook is the class tk::Notebook: pages of widgets, one shown at a
// time, chosen by a row of tabs (Tk's ttk::notebook; docs/future.md 13.3).
var Notebook = &native.Class{Name: "Notebook"}

// notebook is the state of a tk::Notebook besides its widget: the pages in
// the order of their tabs, and the one shown.
type notebook struct {
	nb    *tk.TNotebookWidget
	pages []*widget
	shown int // index of the page shown; -1 while there are none
}

// current is the index of the page Tk shows (-1: none).
func (n *notebook) current() int {
	path := n.nb.Select(nil)
	for i, p := range n.pages {
		if p.w.String() == path {
			return i
		}
	}
	return -1
}

func registerNotebook() {
	// new tk::Notebook(parent [, options]): add(frame, title), select(i or
	// frame), selected(), onchange(f) (f gets the index when the user
	// picks a tab); the options width, height, padding. Its pages are
	// tk::Frame objects made with the notebook as their parent.
	registerWidget(Notebook, widgetSpec{0, 0, false, func(a native.Args, args []value.Value, parent *node, wd *widget, opts []tk.Opt, _ string) error {
		nb := parent.w.TNotebook(opts...)
		wd.w = nb.Window
		wd.book = &notebook{nb: nb, shown: -1}
		tk.Bind(wd.w, "<<NotebookTabChanged>>", tk.Command(func() {
			// Also sent for add and select: the handler is only for a tab
			// the user picked, as for the other widgets.
			i := wd.book.current()
			if i == wd.book.shown {
				return
			}
			wd.book.shown = i
			if i >= 0 && !wd.onchange.IsNil() {
				call(wd.onchange, value.MakeInt(int64(i)))
			}
		}))
		return nil
	}}, func(wd *widget) string {
		n := len(wd.book.pages)
		if n == 1 {
			return "tk::Notebook(1 page)"
		}
		return "tk::Notebook(" + value.MakeInt(int64(n)).String() + " pages)"
	})

	// add(frame, title): frame, a tk::Frame made in this notebook, becomes
	// its last page, with title on its tab. The first page is shown.
	Notebook.Methods["add"] = widgetMethod("Notebook", "add", 2, 2, func(a native.Args, this *value.ObjectObj, wd *widget) (value.Value, error) {
		if !Frame.Is(a.V[0]) {
			return value.NilValue, a.TypeError(0, "a tk::Frame")
		}
		page := a.V[0].AsObject().State.(*widget)
		if page.in != &wd.node {
			return value.NilValue, a.Errorf(value.ValueError, "the frame was not made in this notebook (new tk::Frame(notebook))")
		}
		for _, p := range wd.book.pages {
			if p == page {
				return value.NilValue, a.Errorf(value.ValueError, "the frame is a page already")
			}
		}
		title, err := tkString(a, 1)
		if err != nil {
			return value.NilValue, err
		}
		wd.book.nb.Add(page.w, tk.Txt(title))
		wd.book.pages = append(wd.book.pages, page)
		if wd.book.shown < 0 {
			wd.book.shown = 0
		}
		return value.ObjectValue(this), nil
	})

	// select(i) shows page i (from 0), or select(frame) that page. It
	// calls no handler.
	Notebook.Methods["select"] = widgetMethod("Notebook", "select", 1, 1, func(a native.Args, this *value.ObjectObj, wd *widget) (value.Value, error) {
		i := -1
		switch {
		case a.V[0].Kind() == value.Int:
			n := a.V[0].AsInt()
			if n < 0 || n >= int64(len(wd.book.pages)) {
				return value.NilValue, a.Errorf(value.ValueError, "no page %d (the notebook has %d)", n, len(wd.book.pages))
			}
			i = int(n)
		case Frame.Is(a.V[0]):
			page := a.V[0].AsObject().State.(*widget)
			for j, p := range wd.book.pages {
				if p == page {
					i = j
				}
			}
			if i < 0 {
				return value.NilValue, a.Errorf(value.ValueError, "the frame is not a page of this notebook")
			}
		default:
			return value.NilValue, a.TypeError(0, "an int or a tk::Frame")
		}
		wd.book.shown = i
		wd.book.nb.Select(wd.book.pages[i].w)
		return value.ObjectValue(this), nil
	})

	// selected(): the index of the page shown, nil while there are none.
	// It only reads what the program has: also after the window closed.
	Notebook.Methods["selected"] = func(env value.Env, this *value.ObjectObj, args []value.Value) (value.Value, error) {
		if _, err := native.Check("tk::Notebook::selected", args, 0, 0); err != nil {
			return value.NilValue, err
		}
		if shown := this.State.(*widget).book.shown; shown >= 0 {
			return value.MakeInt(int64(shown)), nil
		}
		return value.NilValue, nil
	}

	Notebook.Methods["onchange"] = handlerSetter("Notebook", "onchange", func(wd *widget) *value.Value { return &wd.onchange })
}
