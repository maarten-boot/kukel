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

	node    *node                 // of the widget: its event map
	binds   map[string][]itemBind // binditem's handlers, by event sequence
	itemTag string                // the binding tag they are bound to
	pressed *item                 // the item a mouse button went down on
}

// canvases counts the canvases with item bindings, for their tags' names.
var canvases int

// itemBind is a handler of binditem: for the item with an id, or the
// items with a tag.
type itemBind struct {
	id  int64
	tag string
	f   value.Value
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

// item is a canvas item: its kind, coordinates, text or image, options and
// tags.
type item struct {
	id     int64
	kind   string // line, rect, oval, arc, polygon, text, image
	coords []float64
	text   string
	img    *picture
	opts   []optionValue // by option, the last value given
	tags   []string
}

func (it *item) hasTag(tag string) bool {
	for _, t := range it.tags {
		if t == tag {
			return true
		}
	}
	return false
}

// option gives the value of the item's option name, or def.
func (it *item) option(name string, def float64) float64 {
	for _, v := range it.opts {
		if v.name == name {
			if x, ok := v.x.(float64); ok {
				return x
			}
		}
	}
	return def
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
	case "arc":
		cv.c.CreateArc(args[0], args[1], args[2], args[3], args[4:]...)
	case "image":
		cv.c.CreateImage(args[0], args[1], append([]any{tk.Image(it.img.img)}, args[2:]...)...)
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

// split takes the items at the positions at (in order) out of the list:
// those, and the others.
func (cv *canvas) split(at []int) (moved, rest []*item) {
	k := 0
	for i, it := range cv.items {
		if k < len(at) && at[k] == i {
			moved = append(moved, it)
			k++
		} else {
			rest = append(rest, it)
		}
	}
	return moved, rest
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
		n := coordsCount[kind]
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
var coordsCount = map[string]int{"line": 1, "polygon": 1, "rect": 4, "oval": 4, "arc": 4, "text": 2, "image": 2}

// degrees is an angle: -360 to 360.
func degrees(v value.Value) (any, string) {
	x, ok := coordinate(v)
	if !ok || math.Abs(x) > 360 {
		return nil, " must be a number of degrees (-360 to 360), not " + v.Repr()
	}
	return x, ""
}

var tagRE = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

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
		"rect": shapeOptions,
		"oval": shapeOptions,
		"arc": append(append([]option{}, shapeOptions...),
			option{"start", degrees, func(x any) tk.Opt { return tk.Start(x) }},
			option{"extent", degrees, func(x any) tk.Opt { return tk.Extent(x) }},
			option{"style", oneOf("pieslice", "chord", "arc"), func(x any) tk.Opt { return tk.Style(x) }}),
		"polygon": shapeOptions,
		"image": {
			{"anchor", oneOf("n", "ne", "e", "se", "s", "sw", "w", "nw", "center"), func(x any) tk.Opt { return tk.Anchor(x) }},
		},
		"text": {
			{"color", color(false), func(x any) tk.Opt { return tk.Fill(x) }},
			{"anchor", oneOf("n", "ne", "e", "se", "s", "sw", "w", "nw", "center"), func(x any) tk.Opt { return tk.Anchor(x) }},
			{"justify", oneOf("left", "center", "right"), func(x any) tk.Opt { return tk.Justify(x) }},
			{"width", pixels, func(x any) tk.Opt { return tk.Width(x) }},
			fontOption(""),
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
// coordinates, for text the text, for an image the tk::Image, then a map
// of options; it gives the item's id.
func create(kind string) value.NativeMethod {
	n := coordsCount[kind]
	if kind == "text" || kind == "image" {
		n++
	}
	return canvasMethod(kind, n, n+1, func(a native.Args, this *value.ObjectObj, cv *canvas) (value.Value, error) {
		coords, err := coordsArgs(a, 0, kind)
		if err != nil {
			return value.NilValue, err
		}
		it := &item{kind: kind, coords: coords}
		if kind == "text" {
			if it.text, err = tkString(a, n-1); err != nil {
				return value.NilValue, err
			}
		}
		if kind == "image" {
			st, err := a.Object(n-1, Image)
			if err != nil {
				return value.NilValue, err
			}
			it.img = st.(*picture)
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
	case "arc": // as the slice: in the oval, and within the angles
		x1, x2 := math.Min(c[0], c[2]), math.Max(c[0], c[2])
		y1, y2 := math.Min(c[1], c[3]), math.Max(c[1], c[3])
		rx, ry := (x2-x1)/2, (y2-y1)/2
		if rx == 0 || ry == 0 {
			return false
		}
		dx, dy := (x-(x1+rx))/rx, (y-(y1+ry))/ry
		if dx*dx+dy*dy > 1 {
			return false
		}
		start, extent := it.option("start", 0), it.option("extent", 90)
		if math.Abs(extent) >= 360 {
			return true
		}
		if extent < 0 {
			start, extent = start+extent, -extent
		}
		angle := math.Atan2(-dy, dx) * 180 / math.Pi // counterclockwise from 3 o'clock, as Tk
		return math.Mod(angle-start+720, 360) <= extent
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
	case "text", "image":
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

// itemAt gives the topmost item at x, y, or nil.
func (cv *canvas) itemAt(x, y float64) *item {
	for i := len(cv.items) - 1; i >= 0; i-- {
		if cv.hit(cv.items[i], x, y) {
			return cv.items[i]
		}
	}
	return nil
}

// targets gives the positions of the items argument i names: an id (that
// item), or a tag (the items with it, bottom to top; maybe none).
func (cv *canvas) targets(a native.Args, i int) ([]int, error) {
	if a.V[i].Kind() == value.String {
		tag, err := tagArg(a, i)
		if err != nil {
			return nil, err
		}
		var at []int
		for j, it := range cv.items {
			if it.hasTag(tag) {
				at = append(at, j)
			}
		}
		return at, nil
	}
	if a.V[i].Kind() != value.Int {
		return nil, a.TypeError(i, "an item's id or a tag")
	}
	j, err := cv.find(a, i)
	return []int{j}, err
}

// tagArg reads argument i, a tag: a name of letters, digits and _ that
// does not start with a digit.
func tagArg(a native.Args, i int) (string, error) {
	tag, err := a.String(i)
	if err == nil && !tagRE.MatchString(tag) {
		err = a.Errorf(value.ValueError, "a tag is a name of letters, digits and _ (not starting with a digit), not %s", value.Quote(tag))
	}
	return tag, err
}

// at gives the item under the mouse at event e.
func (cv *canvas) at(e *tk.Event) *item {
	xv, yv := cv.point(e.X, e.Y)
	x, _ := coordinate(xv)
	y, _ := coordinate(yv)
	return cv.itemAt(x, y)
}

// itemBindings prepares the canvas for binditem: its handlers are bound to
// a binding tag of their own, after the canvas's own (Tk runs the most
// specific binding of each tag, so the canvas's "<Double-Button-1>" does
// not hide an item's "<Button-1>"), and at each press of a mouse button
// the item under the mouse (none: nil) is noted for the events while the
// button is down. The press bindings are those of ButtonPress-1 to -5
// themselves: a general ButtonPress would lose to the program's Button-1.
func (cv *canvas) itemBindings() {
	canvases++
	cv.itemTag = fmt.Sprintf("kukelitems%d", canvases)
	tags := tk.Bindtags(cv.c.Window)
	list := []any{cv.c.Window.String(), cv.itemTag}
	for _, t := range tags {
		if t != cv.c.Window.String() {
			list = append(list, t)
		}
	}
	tk.Bindtags(cv.c.Window, list...)
	for b := 1; b <= 5; b++ {
		tk.BindAppend(cv.itemTag, fmt.Sprintf("<ButtonPress-%d>", b), tk.Command(func(e *tk.Event) { cv.pressed = cv.at(e) }))
	}
}

// dispatch calls the handlers of binditem for event e of sequence seq:
// those of the item under the mouse, or while a button is down (also at
// its release), of the item it went down on, as Tk does for items.
func (cv *canvas) dispatch(seq string, e *tk.Event) {
	const buttons = 0x1f00 // the state bits of mouse buttons 1 to 5
	var it *item
	switch {
	case int(e.State)&buttons == 0:
		it = cv.at(e)
	case cv.pressed != nil && cv.has(cv.pressed):
		it = cv.pressed
	}
	if it == nil {
		return
	}
	for _, b := range cv.binds[seq] {
		if !cv.has(it) {
			return // a handler removed it
		}
		if b.id == it.id || b.tag != "" && it.hasTag(b.tag) {
			em := cv.node.eventMap(e)
			em.AsMap().Set(value.MakeString("item"), value.MakeInt(it.id)) // a string key cannot fail
			call(b.f, em)
		}
	}
}

func (cv *canvas) has(it *item) bool {
	for _, x := range cv.items {
		if x == it {
			return true
		}
	}
	return false
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
			// Without a highlight ring or border, a position in the widget
			// is the same in the drawing (scrolled: plus the view's offset).
			cv.c = in.Canvas(append(append(sb, tk.Highlightthickness(0), tk.Borderwidth(0)), opts...)...)
			return cv.c.Window
		}, func(axis byte, first float64) {
			if axis == 'x' {
				cv.xfirst = first
			} else {
				cv.yfirst = first
			}
		})
		wd.point = cv.point
		cv.node = &wd.node
		cv.setRegion()
		return nil
	}}, func(wd *widget) string {
		if len(wd.canvas.items) == 1 {
			return "tk::Canvas(1 item)"
		}
		return fmt.Sprintf("tk::Canvas(%d items)", len(wd.canvas.items))
	})
	for _, kind := range []string{"line", "rect", "oval", "arc", "polygon", "text", "image"} {
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
	// move(id or tag, dx, dy): the coordinates change; the items stay where
	// they are among the others.
	Canvas.Methods["move"] = canvasMethod("move", 3, 3, func(a native.Args, this *value.ObjectObj, cv *canvas) (value.Value, error) {
		at, err := cv.targets(a, 0)
		if err != nil || len(at) == 0 {
			return value.ObjectValue(this), err
		}
		var d [2]float64
		for j := range d {
			x, ok := coordinate(a.V[1+j])
			if !ok {
				return value.NilValue, a.Errorf(value.ValueError, "argument %d must be a number from -1e6 to 1e6, not %s", j+2, a.V[1+j].Repr())
			}
			d[j] = x
		}
		moved := make([][]float64, len(at))
		for k, i := range at {
			moved[k] = append([]float64(nil), cv.items[i].coords...)
			for j := range moved[k] {
				moved[k][j] += d[j%2]
				if math.Abs(moved[k][j]) > maxCoordinate {
					return value.NilValue, a.Errorf(value.ValueError, "the item would leave the canvas (coordinates from -1e6 to 1e6)")
				}
			}
		}
		for k, i := range at {
			cv.items[i].coords = moved[k]
		}
		cv.redraw(at[0])
		return value.ObjectValue(this), nil
	})
	// configureitem(id or tag, options): each option must be one of every
	// item's kind.
	Canvas.Methods["configureitem"] = canvasMethod("configureitem", 2, 2, func(a native.Args, this *value.ObjectObj, cv *canvas) (value.Value, error) {
		at, err := cv.targets(a, 0)
		if err != nil || len(at) == 0 {
			return value.ObjectValue(this), err
		}
		values := make([][]optionValue, len(at))
		for k, i := range at {
			if values[k], err = readValues(a, 1, canvasItemOptions[cv.items[i].kind]); err != nil {
				return value.NilValue, err
			}
		}
		return value.ObjectValue(this), cv.change(a, at[0], func() func() {
			old := make([][]optionValue, len(at))
			for k, i := range at {
				old[k] = append([]optionValue(nil), cv.items[i].opts...)
				cv.items[i].set(values[k])
			}
			return func() {
				for k, i := range at {
					cv.items[i].opts = old[k]
				}
			}
		})
	})
	// raise(id or tag), lower(id or tag): to the top, to the bottom (the
	// items of a tag keep their order among themselves).
	Canvas.Methods["raise"] = canvasMethod("raise", 1, 1, func(a native.Args, this *value.ObjectObj, cv *canvas) (value.Value, error) {
		at, err := cv.targets(a, 0)
		if err != nil || len(at) == 0 {
			return value.ObjectValue(this), err
		}
		moved, rest := cv.split(at)
		cv.items = append(rest, moved...)
		cv.redraw(at[0])
		return value.ObjectValue(this), nil
	})
	Canvas.Methods["lower"] = canvasMethod("lower", 1, 1, func(a native.Args, this *value.ObjectObj, cv *canvas) (value.Value, error) {
		at, err := cv.targets(a, 0)
		if err != nil || len(at) == 0 {
			return value.ObjectValue(this), err
		}
		moved, rest := cv.split(at)
		cv.items = append(moved, rest...)
		cv.redraw(0)
		return value.ObjectValue(this), nil
	})
	// remove(id or tag), clear()
	Canvas.Methods["remove"] = canvasMethod("remove", 1, 1, func(a native.Args, this *value.ObjectObj, cv *canvas) (value.Value, error) {
		at, err := cv.targets(a, 0)
		if err != nil || len(at) == 0 {
			return value.ObjectValue(this), err
		}
		moved, rest := cv.split(at)
		for _, it := range moved {
			cv.c.Delete(it.tag())
		}
		cv.items = rest
		cv.setRegion()
		return value.ObjectValue(this), nil
	})
	// tag(id, tag), untag(id, tag), tagged(tag): an item's tags, and the
	// ids of the items with a tag (bottom to top).
	Canvas.Methods["tag"] = canvasMethod("tag", 2, 2, func(a native.Args, this *value.ObjectObj, cv *canvas) (value.Value, error) {
		i, err := cv.find(a, 0)
		if err != nil {
			return value.NilValue, err
		}
		tag, err := tagArg(a, 1)
		if err != nil {
			return value.NilValue, err
		}
		if it := cv.items[i]; !it.hasTag(tag) {
			it.tags = append(it.tags, tag)
		}
		return value.ObjectValue(this), nil
	})
	Canvas.Methods["untag"] = canvasMethod("untag", 2, 2, func(a native.Args, this *value.ObjectObj, cv *canvas) (value.Value, error) {
		i, err := cv.find(a, 0)
		if err != nil {
			return value.NilValue, err
		}
		tag, err := tagArg(a, 1)
		if err != nil {
			return value.NilValue, err
		}
		it := cv.items[i]
		for j, t := range it.tags {
			if t == tag {
				it.tags = append(it.tags[:j:j], it.tags[j+1:]...)
				break
			}
		}
		return value.ObjectValue(this), nil
	})
	Canvas.Methods["tagged"] = canvasMethod("tagged", 1, 1, func(a native.Args, this *value.ObjectObj, cv *canvas) (value.Value, error) {
		tag, err := tagArg(a, 0)
		if err != nil {
			return value.NilValue, err
		}
		var v []value.Value
		for _, it := range cv.items {
			if it.hasTag(tag) {
				v = append(v, value.MakeInt(it.id))
			}
		}
		return value.MakeVector(v), nil
	})
	// binditem(id or tag, sequence, f): f(e) for events of sequence on that
	// item or those items (nil: none); e also has "item", the id.
	Canvas.Methods["binditem"] = canvasMethod("binditem", 3, 3, func(a native.Args, this *value.ObjectObj, cv *canvas) (value.Value, error) {
		b := itemBind{}
		var err error
		switch a.V[0].Kind() {
		case value.Int:
			if _, err = cv.find(a, 0); err != nil {
				return value.NilValue, err
			}
			b.id = a.V[0].AsInt()
		case value.String:
			if b.tag, err = tagArg(a, 0); err != nil {
				return value.NilValue, err
			}
		default:
			return value.NilValue, a.TypeError(0, "an item's id or a tag")
		}
		seq, err := a.String(1)
		if err != nil {
			return value.NilValue, err
		}
		if !sequenceRE.MatchString(seq) {
			return value.NilValue, a.Errorf(value.ValueError, "%s is not an event sequence such as \"<Button-1>\" or \"<B1-Motion>\"", value.Quote(seq))
		}
		if b.f, err = handlerArg(a, 2); err != nil {
			return value.NilValue, err
		}
		if cv.binds == nil {
			cv.binds = map[string][]itemBind{}
			cv.itemBindings()
		}
		if _, ok := cv.binds[seq]; !ok {
			tk.BindAppend(cv.itemTag, seq, tk.Command(func(e *tk.Event) { cv.dispatch(seq, e) }))
			if err := tkError(); err != nil {
				return value.NilValue, a.Errorf(TkError, "%v", err)
			}
		}
		list := cv.binds[seq][:0:0]
		for _, old := range cv.binds[seq] {
			if old.id != b.id || old.tag != b.tag {
				list = append(list, old)
			}
		}
		if !b.f.IsNil() {
			list = append(list, b)
		}
		cv.binds[seq] = list
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
		if it := cv.itemAt(p[0], p[1]); it != nil {
			return value.MakeInt(it.id), nil
		}
		return value.NilValue, nil
	})
}
