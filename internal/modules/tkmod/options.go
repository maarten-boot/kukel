//go:build tk

package tkmod

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	tk "modernc.org/tk9.0"

	"github.com/maarten-boot/kukel/internal/native"
	"github.com/maarten-boot/kukel/internal/value"
)

// tkUnsafe tells which byte of s Tk cannot take ("" when none): NUL, and
// the vertical tab and form feed, at which Tcl splits words but which
// tk9.0 does not quote, so that a string with one could add its own
// options to a Tk command (and through them run Tcl code).
func tkUnsafe(s string) string {
	if i := strings.IndexAny(s, "\x00\x0b\x0c"); i >= 0 {
		return fmt.Sprintf("the byte 0x%02x (Tk takes no NUL, vertical tab or form feed)", s[i])
	}
	return ""
}

// tkString reads argument i, a string for Tk.
func tkString(a native.Args, i int) (string, error) {
	s, err := a.String(i)
	if err == nil {
		if bad := tkUnsafe(s); bad != "" {
			err = a.Errorf(value.ValueError, "argument %d holds %s", i+1, bad)
		}
	}
	return s, err
}

// maxPixels and maxCells bound sizes, and rows and columns of a grid.
const (
	maxPixels = 10000
	maxCells  = 10000
)

// An option is one entry of an options map (pack, grid, configure, ...):
// check turns the Kukel value into the value for Tk, or says what is wrong
// with it (the text after "option name"), and make gives the Tk option.
type option struct {
	name  string
	check func(v value.Value) (any, string)
	make  func(any) tk.Opt
}

// readOptions reads argument i, a map of options from specs, into Tk
// options. Nothing is changed when an option is wrong.
func readOptions(a native.Args, i int, specs []option) ([]tk.Opt, error) {
	values, err := readValues(a, i, specs)
	return makeOpts(values), err
}

// makeOpts gives the Tk options of checked values. Some need Tk (a font is
// made), so constructors check their options first and make them once Tk
// runs.
func makeOpts(values []optionValue) []tk.Opt {
	var opts []tk.Opt
	for _, v := range values {
		opts = append(opts, v.make(v.x))
	}
	return opts
}

// A fontSpec is a checked font map; zero parts are the platform's default.
type fontSpec struct {
	family                  string
	size                    int64
	bold, italic, underline bool
}

// fontValue checks a font map: {"family", "size", "bold", "italic",
// "underline"}, each part optional.
func fontValue(v value.Value) (any, string) {
	if v.Kind() != value.Map {
		return nil, " must be a map such as {\"family\": \"Helvetica\", \"size\": 12, \"bold\": true}, not " + v.Repr()
	}
	var f fontSpec
	var bad string
	v.AsMap().Each(func(k, x value.Value) {
		if bad != "" {
			return
		}
		name := ""
		if k.Kind() == value.String {
			name = k.AsString()
		}
		switch name {
		case "family":
			if x.Kind() != value.String || x.AsString() == "" || len(x.AsString()) > 200 || tkUnsafe(x.AsString()) != "" {
				bad = ": family must be the name of a font family, not " + x.Repr()
				return
			}
			f.family = x.AsString()
		case "size":
			if x.Kind() != value.Int || x.AsInt() < 1 || x.AsInt() > 500 {
				bad = ": size must be a number of points (1 to 500), not " + x.Repr()
				return
			}
			f.size = x.AsInt()
		case "bold", "italic", "underline":
			if x.Kind() != value.Bool {
				bad = ": " + name + " must be true or false, not " + x.Repr()
				return
			}
			switch name {
			case "bold":
				f.bold = x.AsBool()
			case "italic":
				f.italic = x.AsBool()
			default:
				f.underline = x.AsBool()
			}
		default:
			bad = ": unknown part " + k.Repr() + " (the parts are family, size, bold, italic and underline)"
		}
	})
	if bad != "" {
		return nil, bad
	}
	return f, ""
}

// fonts are the Tk named fonts made, one per font map; styles the ttk
// styles that carry a font for the classes that take none themselves.
var (
	fonts  = map[fontSpec]*tk.FontFace{}
	styles = map[string]string{}
)

func fontFace(f fontSpec) *tk.FontFace {
	if face := fonts[f]; face != nil {
		return face
	}
	opts := []tk.Opt{tk.Weight(map[bool]string{false: "normal", true: "bold"}[f.bold]),
		tk.Slant(map[bool]string{false: "roman", true: "italic"}[f.italic]),
		tk.Underline(map[bool]int{false: 0, true: 1}[f.underline])}
	if f.family != "" {
		opts = append(opts, tk.Family(f.family))
	}
	if f.size > 0 {
		opts = append(opts, tk.Size(f.size))
	}
	face := tk.NewFont(opts...)
	fonts[f] = face
	return face
}

// fontOption is the option font: given straight to Tk, or (styleClass not
// "": the themed buttons, which take no font) through a ttk style of that
// class with the font.
func fontOption(styleClass string) option {
	return option{"font", fontValue, func(x any) tk.Opt {
		face := fontFace(x.(fontSpec))
		if styleClass == "" {
			return tk.Font(face)
		}
		key := styleClass + " " + face.String()
		style := styles[key]
		if style == "" {
			style = fmt.Sprintf("kukel%d.%s", len(styles)+1, styleClass)
			styles[key] = style
			tk.StyleConfigure(style, tk.Font(face))
		}
		return tk.Style(style)
	}}
}

// An optionValue is an option of an options map with its checked value.
type optionValue struct {
	*option
	x any
}

// readValues reads argument i, a map of options from specs, into checked
// values, in the order of the map.
func readValues(a native.Args, i int, specs []option) ([]optionValue, error) {
	if a.V[i].Kind() != value.Map {
		return nil, a.TypeError(i, "a map of options")
	}
	var values []optionValue
	var bad error
	a.V[i].AsMap().Each(func(k, v value.Value) {
		if bad != nil {
			return
		}
		if k.Kind() != value.String {
			bad = a.Errorf(value.ValueError, "an option is named by a string, not %s", k.Kind())
			return
		}
		name := k.AsString()
		for i := range specs {
			if o := &specs[i]; o.name == name {
				x, wrong := o.check(v)
				if wrong != "" {
					bad = a.Errorf(value.ValueError, "option %s%s", name, wrong)
					return
				}
				values = append(values, optionValue{o, x})
				return
			}
		}
		bad = a.Errorf(value.ValueError, "unknown option %s (the options are %s)", value.Quote(name), names(specs))
	})
	if bad != nil {
		return nil, bad
	}
	return values, nil
}

// names is "a, b and c" of the options' names.
func names(specs []option) string {
	var s []string
	for _, o := range specs {
		s = append(s, o.name)
	}
	if len(s) == 1 {
		return s[0]
	}
	return strings.Join(s[:len(s)-1], ", ") + " and " + s[len(s)-1]
}

// The kinds of option values.

func oneOf(allowed ...string) func(value.Value) (any, string) {
	return func(v value.Value) (any, string) {
		if v.Kind() != value.String {
			return nil, fmt.Sprintf(" must be a string, not %s", v.Kind())
		}
		for _, x := range allowed {
			if v.AsString() == x {
				return x, ""
			}
		}
		return nil, fmt.Sprintf(": %s is not one of %s", value.Quote(v.AsString()), strings.Join(allowed, ", "))
	}
}

func intFrom(min, max int64, what string) func(value.Value) (any, string) {
	return func(v value.Value) (any, string) {
		if v.Kind() != value.Int || v.AsInt() < min || v.AsInt() > max {
			return nil, fmt.Sprintf(" must be %s (%d to %d), not %s", what, min, max, v.Repr())
		}
		return v.AsInt(), ""
	}
}

var (
	pixels     = intFrom(0, maxPixels, "a number of pixels")
	characters = intFrom(0, 1000, "a number of characters")
	cell       = intFrom(0, maxCells, "a row or column number")
	span       = intFrom(1, maxCells, "a number of rows or columns")
	weight     = intFrom(0, 1000, "a weight")
)

func boolean(v value.Value) (any, string) {
	if v.Kind() != value.Bool {
		return nil, fmt.Sprintf(" must be true or false, not %s", v.Kind())
	}
	return v.AsBool(), ""
}

// padding is a number of pixels for all sides, or a vector of 2 (left and
// right, top and bottom) or 4 (left, top, right, bottom).
func padding(v value.Value) (any, string) {
	const want = " must be a number of pixels (0 to 10000) or a vector of 2 or 4 of them"
	if v.Kind() == value.Int {
		if _, wrong := pixels(v); wrong != "" {
			return nil, want + ", not " + v.Repr()
		}
		return v.AsInt(), ""
	}
	if v.Kind() != value.Vector || (len(v.AsVector().Elems) != 2 && len(v.AsVector().Elems) != 4) {
		return nil, want + ", not " + v.Repr()
	}
	var s []string
	for _, e := range v.AsVector().Elems {
		if _, wrong := pixels(e); wrong != "" {
			return nil, want + ", not " + v.Repr()
		}
		s = append(s, fmt.Sprint(e.AsInt()))
	}
	return strings.Join(s, " "), ""
}

// sticky is where a widget sticks in its grid cell: some of n, s, e and w.
func sticky(v value.Value) (any, string) {
	const want = ` must be a string of the sides n, s, e and w (such as "nsew" or "w"), not `
	if v.Kind() != value.String {
		return nil, want + v.Kind().String()
	}
	s := v.AsString()
	for i, c := range s {
		if !strings.ContainsRune("nsew", c) || strings.ContainsRune(s[:i], c) {
			return nil, want + value.Quote(s)
		}
	}
	return s, ""
}

// oneChar is the character an entry shows instead of each one typed (for
// passwords), or "" for the text itself.
func oneChar(v value.Value) (any, string) {
	if v.Kind() != value.String || utf8.RuneCountInString(v.AsString()) > 1 || !utf8.ValidString(v.AsString()) || tkUnsafe(v.AsString()) != "" {
		return nil, " must be one character, or \"\" to show the text, not " + v.Repr()
	}
	return v.AsString(), ""
}

var groupRE = regexp.MustCompile(`^[A-Za-z0-9_]*$`)

// group is the name of a group of rows or columns of the same size.
func group(v value.Value) (any, string) {
	if v.Kind() != value.String || !groupRE.MatchString(v.AsString()) {
		return nil, " must be a name of letters, digits and _ (\"\" for none), not " + v.Repr()
	}
	return v.AsString(), ""
}

// The options of pack, grid, rowconfigure and columnconfigure.
var (
	packOptions = []option{
		{"side", oneOf("top", "bottom", "left", "right"), func(x any) tk.Opt { return tk.Side(x) }},
		{"fill", oneOf("none", "x", "y", "both"), func(x any) tk.Opt { return tk.Fill(x) }},
		{"expand", boolean, func(x any) tk.Opt { return tk.Expand(x) }},
		{"padx", pixels, func(x any) tk.Opt { return tk.Padx(x) }},
		{"pady", pixels, func(x any) tk.Opt { return tk.Pady(x) }},
	}
	gridOptions = []option{
		{"row", cell, func(x any) tk.Opt { return tk.Row(x) }},
		{"column", cell, func(x any) tk.Opt { return tk.Column(x) }},
		{"rowspan", span, func(x any) tk.Opt { return tk.Rowspan(x) }},
		{"columnspan", span, func(x any) tk.Opt { return tk.Columnspan(x) }},
		{"sticky", sticky, func(x any) tk.Opt { return tk.Sticky(x) }},
		{"padx", pixels, func(x any) tk.Opt { return tk.Padx(x) }},
		{"pady", pixels, func(x any) tk.Opt { return tk.Pady(x) }},
	}
	gridConfigureOptions = []option{
		{"weight", weight, func(x any) tk.Opt { return tk.Weight(x) }},
		{"minsize", pixels, func(x any) tk.Opt { return tk.Minsize(x) }},
		{"uniform", group, func(x any) tk.Opt { return tk.Uniform(x) }},
	}
)

// The options of configure, by class (the text is not one: settext).
var (
	optWidth      = option{"width", characters, func(x any) tk.Opt { return tk.Width(x) }}
	optPadding    = option{"padding", padding, func(x any) tk.Opt { return tk.Padding(x) }}
	optJustify    = option{"justify", oneOf("left", "center", "right"), func(x any) tk.Opt { return tk.Justify(x) }}
	optState      = option{"state", oneOf("normal", "disabled"), func(x any) tk.Opt { return tk.State(x) }}
	configOptions = map[string][]option{
		"Frame": {
			{"width", pixels, func(x any) tk.Opt { return tk.Width(x) }},
			{"height", pixels, func(x any) tk.Opt { return tk.Height(x) }},
			optPadding,
			{"relief", oneOf("flat", "raised", "sunken", "groove", "ridge", "solid"), func(x any) tk.Opt { return tk.Relief(x) }},
			{"borderwidth", pixels, func(x any) tk.Opt { return tk.Borderwidth(x) }},
		},
		"Label": {
			optWidth,
			fontOption(""),
			{"anchor", oneOf("n", "ne", "e", "se", "s", "sw", "w", "nw", "center"), func(x any) tk.Opt { return tk.Anchor(x) }},
			optJustify,
			{"wraplength", pixels, func(x any) tk.Opt { return tk.Wraplength(x) }},
			optPadding,
			optState,
		},
		"Button": {optWidth, optPadding, optState, fontOption("TButton")},
		"Entry": {
			optWidth,
			fontOption(""),
			optJustify,
			{"show", oneChar, func(x any) tk.Opt { return tk.Show(x) }},
			{"state", oneOf("normal", "disabled", "readonly"), func(x any) tk.Opt { return tk.State(x) }},
		},
		"Text": {
			optWidth,
			optHeight,
			fontOption(""),
			{"wrap", oneOf("none", "char", "word"), func(x any) tk.Opt { return tk.Wrap(x) }},
			optState,
		},
		"Listbox": {
			optWidth,
			optHeight,
			fontOption(""),
			{"selectmode", oneOf("browse", "single", "extended", "multiple"), func(x any) tk.Opt { return tk.Selectmode(x) }},
			optState,
		},
		"Checkbutton": {optWidth, optPadding, optState, fontOption("TCheckbutton")},
		"Canvas": {
			{"width", pixels, func(x any) tk.Opt { return tk.Width(x) }},
			{"height", pixels, func(x any) tk.Opt { return tk.Height(x) }},
			{"background", color(false), func(x any) tk.Opt { return tk.Background(x) }},
		},
		"Radiobutton": {optWidth, optPadding, optState, fontOption("TRadiobutton")},
	}
	optHeight = option{"height", intFrom(1, 1000, "a number of lines"), func(x any) tk.Opt { return tk.Height(x) }}
)

// mapGet gives the value of key in the map m (nil when there is none).
func mapGet(m value.Value, key string) value.Value {
	v, _, _ := m.AsMap().Get(value.MakeString(key)) // a string key cannot fail
	return v
}

// scrollOption takes the option scroll ("none", "x", "y" or "both") out
// of the options map that is argument i: it gives the arguments with that
// map without it, for readOptions.
func scrollOption(a native.Args, i int) (native.Args, string, error) {
	v := mapGet(a.V[i], "scroll")
	if v.IsNil() {
		return a, "none", nil
	}
	x, wrong := oneOf("none", "x", "y", "both")(v)
	if wrong != "" {
		return a, "", a.Errorf(value.ValueError, "option scroll%s", wrong)
	}
	rest := value.NewMap()
	a.V[i].AsMap().Each(func(k, v value.Value) {
		if k.Kind() != value.String || k.AsString() != "scroll" {
			rest.Set(k, v) // keys of a map cannot fail
		}
	})
	b := a
	b.V = append([]value.Value(nil), a.V...)
	b.V[i] = value.MakeMap(rest)
	return b, x.(string), nil
}
