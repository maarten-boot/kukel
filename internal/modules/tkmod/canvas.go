//go:build tk

package tkmod

import (
	"fmt"
	"math"
	"regexp"
	"strconv"

	tk "modernc.org/tk9.0"

	"github.com/maarten-boot/kukel/internal/native"
	"github.com/maarten-boot/kukel/internal/value"
)

// Canvas is the class tk::Canvas: a surface to draw lines, rectangles,
// ovals, polygons and text on. It is a classic Tk widget.
var Canvas = &native.Class{Name: "Canvas"}

// The items of a canvas are kept here too: tk9.0 can create and delete
// canvas items, and give their bounding box, but has nothing to move them
// or change them (and no raw Tcl, which is good: no program can inject
// any). So a change deletes the item and the items above it and draws
// them again, in the same order; each item has the tag k<id>.

// canvas is the drawing of a tk::Canvas.
type canvas struct {
	c      *tk.CanvasWidget
	scroll string
	items  []*item // from the bottom to the top
	next   int64

	// What the scrollbars reach, and the fractions of it left of and
	// above the view: for the coordinates of a position in the widget.
	region         [4]float64
	xfirst, yfirst float64
}

// point turns the position x, y in the widget into canvas coordinates.
func (cv *canvas) point(x, y int) (value.Value, value.Value) {
	if cv.scroll == "none" {
		return value.MakeInt(int64(x)), value.MakeInt(int64(y))
	}
	r := cv.region
	return number(r[0] + cv.xfirst*(r[2]-r[0]) + float64(x)), number(r[1] + cv.yfirst*(r[3]-r[1]) + float64(y))
}

// number is x as an int when it is whole, else as a float.
func number(x float64) value.Value {
	if x == math.Trunc(x) && math.Abs(x) < 1e15 {
		return value.MakeInt(int64(x))
	}
	return value.MakeFloat(x)
}

// item is a canvas item: its kind, coordinates, text and options.
type item struct {
	id     int64
	kind   string // line, rect, oval, polygon, text
	coords []float64
	text   string
	opts   []optionValue // by option, the last value given
}

func (it *item) tag() string { return "k" + strconv.FormatInt(it.id, 10) }

// set merges options into the item's options.
func (it *item) set(values []optionValue) {
next:
	for _, v := range values {
		for i := range it.opts {
			if it.opts[i].name == v.name {
				it.opts[i] = v
				continue next
			}
		}
		it.opts = append(it.opts, v)
	}
}

// draw makes the item in Tk.
func (cv *canvas) draw(it *item) {
	args := []any{}
	for _, x := range it.coords {
		args = append(args, x)
	}
	for _, v := range it.opts {
		args = append(args, v.make(v.x))
	}
	args = append(args, tk.Tags(it.tag()))
	switch it.kind {
	case "line":
		cv.c.CreateLine(args[0], args[1], args[2:]...)
	case "rect":
		cv.c.CreateRectangle(args[0], args[1], args[2], args[3], args[4:]...)
	case "oval":
		cv.c.CreateOval(args[0], args[1], args[2], args[3], args[4:]...)
	case "polygon":
		cv.c.CreatePolygon(args[0], args[1], args[2:]...)
	case "text":
		cv.c.CreateText(args[0], args[1], append([]any{tk.Txt(it.text)}, args[2:]...)...)
	}
}

// redraw draws the items from position i up again (the item at i changed,
// or moved there).
func (cv *canvas) redraw(i int) {
	for _, it := range cv.items[i:] {
		cv.c.Delete(it.tag())
	}
	for _, it := range cv.items[i:] {
		cv.draw(it)
	}
	cv.setRegion()
}

// setRegion makes everything drawn, and the origin, reachable with the
// scrollbars (with the origin in it, a drawing smaller than the view shows
// at its own coordinates).
func (cv *canvas) setRegion() {
	if cv.scroll == "none" {
		return
	}
	r := [4]float64{}
	if box := cv.c.Bbox("all"); len(box) == 4 {
		for i, s := range box {
			x, _ := strconv.ParseFloat(s, 64)
			if i < 2 {
				r[i] = math.Min(0, x)
			} else {
				r[i] = math.Max(0, x)
			}
		}
	}
	cv.region = r
	cv.c.Configure(tk.Scrollregion(fmt.Sprintf("%g %g %g %g", r[0], r[1], r[2], r[3])))
}

// change makes a change of the items from position i up and draws them
// again; when Tk refuses (an unknown colour name), it undoes the change.
func (cv *canvas) change(a native.Args, i int, change func() (undo func())) error {
	undo := change()
	cv.redraw(i)
	if err := tkError(); err != nil {
		undo()
		cv.redraw(i)
		tkError()
		return a.Errorf(TkError, "%v", err)
	}
	return nil
}

// find gives the position of the item with the id of argument i.
func (cv *canvas) find(a native.Args, i int) (int, error) {
	id, err := a.Int(i)
	if err != nil {
		return 0, err
	}
	for j, it := range cv.items {
		if it.id == id {
			return j, nil
		}
	}
	return 0, a.Errorf(value.ValueError, "the canvas has no item %d", id)
}

// A coordinate is a number from -1e6 to 1e6.
const maxCoordinate = 1e6

func coordinate(v value.Value) (float64, bool) {
	var x float64
	switch v.Kind() {
	case value.Int:
		x = float64(v.AsInt())
	case value.Float:
		x = v.AsFloat()
	default:
		return 0, false
	}
	return x, !math.IsNaN(x) && math.Abs(x) <= maxCoordinate
}

// coordsArgs reads the coordinates of an item of kind: args i to i+n-1
// (rect, oval: x1, y1, x2, y2; text: x, y), or a vector of points at i
// (line: at least 2, polygon: at least 3).
func coordsArgs(a native.Args, i int, kind string) ([]float64, error) {
	var vals []value.Value
	var want string
	switch kind {
	case "line", "polygon":
		min := map[string]int{"line": 2, "polygon": 3}[kind]
		want = fmt.Sprintf("a vector of at least %d points [x1, y1, x2, y2, ...] (numbers from -1e6 to 1e6)", min)
		if a.V[i].Kind() != value.Vector {
			return nil, a.TypeError(i, want)
		}
		vals = a.V[i].AsVector().Elems
		if len(vals) < 2*min || len(vals)%2 != 0 {
			return nil, a.Errorf(value.ValueError, "argument %d must be %s, not %s", i+1, want, a.V[i].Repr())
		}
	default:
		n := map[string]int{"rect": 4, "oval": 4, "text": 2}[kind]
		vals = a.V[i : i+n]
		want = "a number from -1e6 to 1e6"
	}
	coords := make([]float64, len(vals))
	for j, v := range vals {
		x, ok := coordinate(v)
		if !ok {
			if kind == "line" || kind == "polygon" {
				return nil, a.Errorf(value.ValueError, "argument %d must be %s, not %s", i+1, want, a.V[i].Repr())
			}
			return nil, a.Errorf(value.ValueError, "argument %d must be %s, not %s", i+j+1, want, v.Repr())
		}
		coords[j] = x
	}
	return coords, nil
}

// coordsCount is how many arguments the coordinates of kind take.
var coordsCount = map[string]int{"line": 1, "polygon": 1, "rect": 4, "oval": 4, "text": 2}

var colorRE = regexp.MustCompile(`^(?:#(?:[0-9A-Fa-f]{3}|[0-9A-Fa-f]{6}|[0-9A-Fa-f]{12})|[A-Za-z][A-Za-z0-9 ]*)$`)

// color is a colour: a name such as "red" or "light blue", or "#rgb",
// "#rrggbb"; with none, also "" (not drawn).
func color(none bool) func(value.Value) (any, string) {
	return func(v value.Value) (any, string) {
		want := ` must be a colour, such as "red" or "#ff8000"`
		if none {
			want += ` (or "" for none)`
		}
		if v.Kind() != value.String || !(colorRE.MatchString(v.AsString()) || none && v.AsString() == "") {
			return nil, want + ", not " + v.Repr()
		}
		return v.AsString(), ""
	}
}

// The options of the items, by kind.
var (
	optLineWidth      = option{"width", intFrom(0, 1000, "a number of pixels"), func(x any) tk.Opt { return tk.Width(x) }}
	optFill           = option{"fill", color(true), func(x any) tk.Opt { return tk.Fill(x) }}
	optOutline        = option{"outline", color(true), func(x any) tk.Opt { return tk.Outline(x) }}
	shapeOptions      = []option{optFill, optOutline, optLineWidth}
	canvasItemOptions = map[string][]option{
		"line": {
			{"color", color(false), func(x any) tk.Opt { return tk.Fill(x) }},
			optLineWidth,
			{"arrow", oneOf("none", "first", "last", "both"), func(x any) tk.Opt { return tk.Linearrow(x) }},
			{"smooth", boolean, func(x any) tk.Opt { return tk.Smooth(x) }},
		},
		"rect":    shapeOptions,
		"oval":    shapeOptions,
		"polygon": shapeOptions,
		"text": {
			{"color", color(false), func(x any) tk.Opt { return tk.Fill(x) }},
			{"anchor", oneOf("n", "ne", "e", "se", "s", "sw", "w", "nw", "center"), func(x any) tk.Opt { return tk.Anchor(x) }},
			{"justify", oneOf("left", "center", "right"), func(x any) tk.Opt { return tk.Justify(x) }},
			{"width", pixels, func(x any) tk.Opt { return tk.Width(x) }},
		},
	}
)

// canvasMethod makes a member function of a canvas.
func canvasMethod(name string, min, max int, fn func(a native.Args, this *value.ObjectObj, cv *canvas) (value.Value, error)) value.NativeMethod {
	return widgetMethod("Canvas", name, min, max, func(a native.Args, this *value.ObjectObj, wd *widget) (value.Value, error) {
		return fn(a, this, wd.canvas)
	})
}

// create makes the member function that draws an item of kind: its
// coordinates, for text the text, then a map of options; it gives the
// item's id.
func create(kind string) value.NativeMethod {
	n := coordsCount[kind]
	if kind == "text" {
		n++
	}
	return canvasMethod(kind, n, n+1, func(a native.Args, this *value.ObjectObj, cv *canvas) (value.Value, error) {
		coords, err := coordsArgs(a, 0, kind)
		if err != nil {
			return value.NilValue, err
		}
		it := &item{kind: kind, coords: coords}
		if kind == "text" {
			if it.text, err = a.String(n - 1); err != nil {
				return value.NilValue, err
			}
		}
		if a.Has(n) {
			values, err := readValues(a, n, canvasItemOptions[kind])
			if err != nil {
				return value.NilValue, err
			}
			it.set(values)
		}
		it.id = cv.next + 1
		cv.draw(it)
		if err := tkError(); err != nil { // an unknown colour name
			cv.c.Delete(it.tag())
			tkError()
			return value.NilValue, a.Errorf(TkError, "%v", err)
		}
		cv.next++
		cv.items = append(cv.items, it)
		cv.setRegion()
		return value.MakeInt(it.id), nil
	})
}

// hit tells whether the point x, y is on the item (for a rectangle, an
// oval or a polygon inside it, for a line within half its width (at least
// 3 pixels), for text inside its box).
func (cv *canvas) hit(it *item, x, y float64) bool {
	c := it.coords
	switch it.kind {
	case "rect", "oval":
		x1, x2 := math.Min(c[0], c[2]), math.Max(c[0], c[2])
		y1, y2 := math.Min(c[1], c[3]), math.Max(c[1], c[3])
		if it.kind == "rect" {
			return x >= x1 && x <= x2 && y >= y1 && y <= y2
		}
		rx, ry := (x2-x1)/2, (y2-y1)/2
		if rx == 0 || ry == 0 {
			return false
		}
		dx, dy := (x-(x1+rx))/rx, (y-(y1+ry))/ry
		return dx*dx+dy*dy <= 1
	case "line":
		reach := 3.0
		for _, v := range it.opts {
			if v.name == "width" {
				reach = math.Max(reach, float64(v.x.(int64))/2)
			}
		}
		for i := 0; i+3 < len(c); i += 2 {
			if segmentDistance(x, y, c[i], c[i+1], c[i+2], c[i+3]) <= reach {
				return true
			}
		}
		return false
	case "polygon":
		inside := false
		for i, j := 0, len(c)-2; i < len(c); j, i = i, i+2 {
			if (c[i+1] > y) != (c[j+1] > y) && x < (c[j]-c[i])*(y-c[i+1])/(c[j+1]-c[i+1])+c[i] {
				inside = !inside
			}
		}
		return inside
	case "text":
		box := cv.c.Bbox(it.tag())
		if len(box) != 4 {
			return false
		}
		var b [4]float64
		for i, s := range box {
			b[i], _ = strconv.ParseFloat(s, 64)
		}
		return x >= b[0] && x <= b[2] && y >= b[1] && y <= b[3]
	}
	return false
}

// segmentDistance is the distance of the point x, y to the segment from
// x1, y1 to x2, y2.
func segmentDistance(x, y, x1, y1, x2, y2 float64) float64 {
	dx, dy := x2-x1, y2-y1
	t := 0.0
	if l := dx*dx + dy*dy; l > 0 {
		t = math.Max(0, math.Min(1, ((x-x1)*dx+(y-y1)*dy)/l))
	}
	return math.Hypot(x-(x1+t*dx), y-(y1+t*dy))
}

func pointsValue(coords []float64) value.Value {
	v := make([]value.Value, len(coords))
	for i, x := range coords {
		v[i] = number(x)
	}
	return value.MakeVector(v)
}

func registerCanvas() {
	// new tk::Canvas(parent [, options]): the options width, height,
	// background; scroll in the constructor.
	registerWidget(Canvas, widgetSpec{0, 0, true, func(a native.Args, args []value.Value, parent *node, wd *widget, opts []tk.Opt, scroll string) error {
		cv := &canvas{scroll: scroll}
		wd.canvas = cv
		wd.w, wd.outer = scrolled(parent, scroll, func(in *tk.Window, sb ...tk.Opt) *tk.Window {
			cv.c = in.Canvas(append(sb, opts...)...)
			return cv.c.Window
		}, func(axis byte, first float64) {
			if axis == 'x' {
				cv.xfirst = first
			} else {
				cv.yfirst = first
			}
		})
		wd.point = cv.point
		cv.setRegion()
		return nil
	}}, func(wd *widget) string {
		if len(wd.canvas.items) == 1 {
			return "tk::Canvas(1 item)"
		}
		return fmt.Sprintf("tk::Canvas(%d items)", len(wd.canvas.items))
	})
	for _, kind := range []string{"line", "rect", "oval", "polygon", "text"} {
		Canvas.Methods[kind] = create(kind)
	}
	// coords(id): the item's coordinates, as a vector.
	Canvas.Methods["coords"] = canvasMethod("coords", 1, 1, func(a native.Args, this *value.ObjectObj, cv *canvas) (value.Value, error) {
		i, err := cv.find(a, 0)
		if err != nil {
			return value.NilValue, err
		}
		return pointsValue(cv.items[i].coords), nil
	})
	// setcoords(id, coordinates...): as the item was made with.
	Canvas.Methods["setcoords"] = canvasMethod("setcoords", 2, 5, func(a native.Args, this *value.ObjectObj, cv *canvas) (value.Value, error) {
		i, err := cv.find(a, 0)
		if err != nil {
			return value.NilValue, err
		}
		it := cv.items[i]
		if n := coordsCount[it.kind]; len(a.V) != 1+n {
			return value.NilValue, a.Errorf(value.ValueError, "a %s takes %d coordinate arguments, not %d", it.kind, n, len(a.V)-1)
		}
		coords, err := coordsArgs(a, 1, it.kind)
		if err != nil {
			return value.NilValue, err
		}
		it.coords = coords
		cv.redraw(i)
		return value.ObjectValue(this), nil
	})
	// move(id, dx, dy): the coordinates change; the item stays where it is
	// among the others.
	Canvas.Methods["move"] = canvasMethod("move", 3, 3, func(a native.Args, this *value.ObjectObj, cv *canvas) (value.Value, error) {
		i, err := cv.find(a, 0)
		if err != nil {
			return value.NilValue, err
		}
		var d [2]float64
		for j := range d {
			x, ok := coordinate(a.V[1+j])
			if !ok {
				return value.NilValue, a.Errorf(value.ValueError, "argument %d must be a number from -1e6 to 1e6, not %s", j+2, a.V[1+j].Repr())
			}
			d[j] = x
		}
		it := cv.items[i]
		moved := append([]float64(nil), it.coords...)
		for j := range moved {
			moved[j] += d[j%2]
			if math.Abs(moved[j]) > maxCoordinate {
				return value.NilValue, a.Errorf(value.ValueError, "the item would leave the canvas (coordinates from -1e6 to 1e6)")
			}
		}
		it.coords = moved
		cv.redraw(i)
		return value.ObjectValue(this), nil
	})
	// configureitem(id, options)
	Canvas.Methods["configureitem"] = canvasMethod("configureitem", 2, 2, func(a native.Args, this *value.ObjectObj, cv *canvas) (value.Value, error) {
		i, err := cv.find(a, 0)
		if err != nil {
			return value.NilValue, err
		}
		values, err := readValues(a, 1, canvasItemOptions[cv.items[i].kind])
		if err != nil {
			return value.NilValue, err
		}
		it := cv.items[i]
		return value.ObjectValue(this), cv.change(a, i, func() func() {
			old := append([]optionValue(nil), it.opts...)
			it.set(values)
			return func() { it.opts = old }
		})
	})
	// raise(id), lower(id): to the top, to the bottom.
	Canvas.Methods["raise"] = canvasMethod("raise", 1, 1, func(a native.Args, this *value.ObjectObj, cv *canvas) (value.Value, error) {
		i, err := cv.find(a, 0)
		if err != nil {
			return value.NilValue, err
		}
		it := cv.items[i]
		cv.items = append(append(cv.items[:i:i], cv.items[i+1:]...), it)
		cv.redraw(i)
		return value.ObjectValue(this), nil
	})
	Canvas.Methods["lower"] = canvasMethod("lower", 1, 1, func(a native.Args, this *value.ObjectObj, cv *canvas) (value.Value, error) {
		i, err := cv.find(a, 0)
		if err != nil {
			return value.NilValue, err
		}
		it := cv.items[i]
		cv.items = append([]*item{it}, append(cv.items[:i:i], cv.items[i+1:]...)...)
		cv.redraw(0)
		return value.ObjectValue(this), nil
	})
	// remove(id), clear()
	Canvas.Methods["remove"] = canvasMethod("remove", 1, 1, func(a native.Args, this *value.ObjectObj, cv *canvas) (value.Value, error) {
		i, err := cv.find(a, 0)
		if err != nil {
			return value.NilValue, err
		}
		cv.c.Delete(cv.items[i].tag())
		cv.items = append(cv.items[:i:i], cv.items[i+1:]...)
		cv.setRegion()
		return value.ObjectValue(this), nil
	})
	Canvas.Methods["clear"] = canvasMethod("clear", 0, 0, func(a native.Args, this *value.ObjectObj, cv *canvas) (value.Value, error) {
		cv.c.Delete("all")
		cv.items = nil
		cv.setRegion()
		return value.ObjectValue(this), nil
	})
	// items(): the ids, from the bottom to the top.
	Canvas.Methods["items"] = canvasMethod("items", 0, 0, func(a native.Args, this *value.ObjectObj, cv *canvas) (value.Value, error) {
		v := make([]value.Value, len(cv.items))
		for i, it := range cv.items {
			v[i] = value.MakeInt(it.id)
		}
		return value.MakeVector(v), nil
	})
	// itemat(x, y): the id of the topmost item at that point, or nil.
	Canvas.Methods["itemat"] = canvasMethod("itemat", 2, 2, func(a native.Args, this *value.ObjectObj, cv *canvas) (value.Value, error) {
		var p [2]float64
		for j := range p {
			x, ok := coordinate(a.V[j])
			if !ok {
				return value.NilValue, a.Errorf(value.ValueError, "argument %d must be a number from -1e6 to 1e6, not %s", j+1, a.V[j].Repr())
			}
			p[j] = x
		}
		for i := len(cv.items) - 1; i >= 0; i-- {
			if cv.hit(cv.items[i], p[0], p[1]) {
				return value.MakeInt(cv.items[i].id), nil
			}
		}
		return value.NilValue, nil
	})
}
