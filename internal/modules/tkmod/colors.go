//go:build tk

package tkmod

import (
	"fmt"
	"strings"

	tk "modernc.org/tk9.0"

	"github.com/maarten-boot/kukel/internal/native"
	"github.com/maarten-boot/kukel/internal/value"
)

// colorKeys are the parts of the palette of tk::setcolors, in the order
// tk::colors() gives them.
var colorKeys = []string{"background", "foreground", "field", "select", "selectforeground", "trough", "border", "active", "disabled"}

// palette holds the colours tk::setcolors was given (by key); the parts
// never given stay the theme's.
var palette = map[string]string{}

// classic are the classic widgets (texts and list boxes) that are open:
// ttk styles do not reach them, so setcolors configures each.
var classic = map[*widget]bool{}

// frameStyles are the ttk styles made for the option background of a
// frame, by colour.
var frameStyles = map[string]string{}

// probe is a classic frame never shown: configuring its background is
// how a colour name is checked, since ttk styles take any name and fail
// only when drawn.
var probe *tk.Window

// knownColor tells whether Tk knows colour c (a name or #rgb form); when
// not, tk.Error holds Tk's message.
func knownColor(c string) bool {
	if probe == nil {
		probe = tk.App.Frame().Window
	}
	probe.Configure(tk.Background(c))
	return tk.Error == nil
}

// frameStyle gives the ttk style of a frame with background colour c (an
// unknown colour leaves the frame as it was, with tk.Error set).
func frameStyle(c string) tk.Opt {
	style := frameStyles[c]
	if style == "" {
		if !knownColor(c) {
			return tk.Style("TFrame")
		}
		style = fmt.Sprintf("kukelbg%d.TFrame", len(frameStyles)+1)
		frameStyles[c] = style
		tk.StyleConfigure(style, tk.Background(c))
	}
	return tk.Style(style)
}

// applyColors gives the palette to Tk: the ttk styles (all themed widgets,
// with the state maps of the clam theme's classes for active and disabled
// widgets), the windows and the classic widgets. It runs again after
// tk::settheme, whose styles start from the theme's own.
func applyColors() {
	p := palette
	var dot []any
	set := func(key string, opt func(any) tk.Opt) {
		if c := p[key]; c != "" {
			dot = append(dot, opt(c))
		}
	}
	set("background", tk.Background)
	set("background", tk.Lightcolor)
	set("background", tk.Darkcolor)
	set("foreground", tk.Foreground)
	set("foreground", tk.Insertcolor)
	set("foreground", tk.Arrowcolor)
	set("field", tk.Fieldbackground)
	set("select", tk.Selectbackground)
	set("selectforeground", tk.Selectforeground)
	set("trough", tk.Troughcolor)
	set("border", tk.Bordercolor)
	set("border", tk.Focuscolor)
	if len(dot) > 0 {
		tk.StyleConfigure(".", dot...)
	}
	bg, active := p["background"], p["active"]
	if active == "" {
		active = bg
	}
	if active != "" {
		disabledBg := bg
		if disabledBg == "" {
			disabledBg = active
		}
		for _, class := range []string{"TButton", "TCheckbutton", "TRadiobutton", "TMenubutton"} {
			tk.StyleMap(class, tk.Background, "disabled", disabledBg, "pressed", active, "active", active)
		}
		tk.StyleMap("TCombobox", tk.Background, "disabled", disabledBg, "pressed", active, "active", active)
		tk.StyleMap("TScrollbar", tk.Background, "disabled", disabledBg, "pressed", active, "active", active)
		tk.StyleMap("Horizontal.TScale", tk.Background, "disabled", disabledBg, "pressed", active, "active", active)
		tk.StyleMap("Vertical.TScale", tk.Background, "disabled", disabledBg, "pressed", active, "active", active)
		tk.StyleMap("TNotebook.Tab", tk.Background, "selected", disabledBg, "!selected", active)
	}
	if field := p["field"]; field != "" {
		readonly := bg
		if readonly == "" {
			readonly = field
		}
		tk.StyleMap("TCombobox", tk.Fieldbackground, "readonly", readonly, "disabled", readonly)
		tk.StyleMap("TEntry", tk.Fieldbackground, "readonly", readonly, "disabled", readonly)
	}
	if sel, fg := p["select"], p["selectforeground"]; sel != "" && fg != "" {
		tk.StyleMap("TCombobox", tk.Selectbackground, "!focus", sel, tk.Selectforeground, "!focus", fg)
	}
	if c := p["disabled"]; c != "" {
		tk.StyleMap(".", tk.Foreground, "disabled", c)
	}
	if bg != "" {
		for win := range gui.open {
			win.w.Configure(tk.Background(bg))
		}
	}
	for wd := range classic {
		if wd.win.closed {
			delete(classic, wd)
			continue
		}
		wd.w.Configure(classicColors(wd.class)...)
	}
}

// classicColors are the palette's options for a text or list box (class
// "Text" or "Listbox").
func classicColors(class string) []tk.Opt {
	var opts []tk.Opt
	add := func(key string, opt func(any) tk.Opt) {
		if c := palette[key]; c != "" {
			opts = append(opts, opt(c))
		}
	}
	add("field", tk.Background)
	add("foreground", tk.Foreground)
	add("select", tk.Selectbackground)
	add("selectforeground", tk.Selectforeground)
	add("background", tk.Highlightbackground)
	add("border", tk.Highlightcolor)
	if class == "Listbox" { // a text has no colour of its own when disabled
		add("disabled", tk.Disabledforeground)
	} else { // a list box has no cursor
		add("foreground", tk.Insertbackground)
	}
	return opts
}

// colorFuncs are tk::setcolors(map) and tk::colors().
func colorFuncs(funcs map[string]native.Func) {
	funcs["setcolors"] = themeFunc("setcolors", 1, func(a native.Args) (value.Value, error) {
		if a.V[0].Kind() != value.Map {
			return value.NilValue, a.TypeError(0, "a map of colours")
		}
		check := color(false)
		next := map[string]string{}
		var bad error
		a.V[0].AsMap().Each(func(k, v value.Value) {
			if bad != nil {
				return
			}
			name := ""
			if k.Kind() == value.String {
				name = k.AsString()
			}
			known := false
			for _, key := range colorKeys {
				known = known || key == name
			}
			if !known {
				bad = a.Errorf(value.ValueError, "unknown colour %s (the colours are %s)", k.Repr(), strings.Join(colorKeys, ", "))
				return
			}
			c, wrong := check(v)
			if wrong != "" {
				bad = a.Errorf(value.ValueError, "colour %s%s", name, wrong)
				return
			}
			next[name] = c.(string)
		})
		if bad != nil {
			return value.NilValue, bad
		}
		for _, k := range colorKeys { // in a fixed order: the first unknown one is named
			if c, ok := next[k]; ok && !knownColor(c) {
				err := tkError()
				return value.NilValue, a.Errorf(TkError, "colour %s: %v", k, err)
			}
		}
		for k, c := range next {
			palette[k] = c
		}
		applyColors()
		return value.NilValue, nil
	})
	funcs["colors"] = func(env value.Env, args []value.Value) (value.Value, error) {
		if _, err := native.Check("tk::colors", args, 0, 0); err != nil {
			return value.NilValue, err
		}
		m := value.NewMap()
		for _, k := range colorKeys {
			if c := palette[k]; c != "" {
				m.Set(value.MakeString(k), value.MakeString(c))
			}
		}
		return value.MakeMap(m), nil
	}
}
