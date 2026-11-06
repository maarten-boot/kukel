# 21. Windows

[The booklet](README.md) · previous: [20. The tools](20-tools.md) · next: Appendices (to come)

The module `tk` opens windows on the desktop, with buttons, text fields,
lists, menus, dialogs and a canvas to draw on. It uses Tk, the toolkit of
Tcl, which comes inside kukel: nothing else needs to be installed.

Tk adds about 19 MB, so it is only in `kukel-tk`, the larger of the two
downloads (or a kukel built with `-tags tk`). In the other, `use tk;` is
a compile error that says so. `kukel version` lists `tk` among the
modules when it is there. A window needs a display: on Linux, a desktop
session (`$DISPLAY`).

## A first window

```
use tk;
use events;

main() {
    local w = new tk::Window("Counter"), n = 0;
    local label = new tk::Label(w, "clicked 0 times", {"width": 20});
    local button = new tk::Button(w, "Click me");
    button->onclick(fn() {
        n++;
        label->settext("clicked " + str(n) + " times");
    });
    label->pack({"padx": 10, "pady": 5});
    button->pack({"pady": 5});
    events::run();                           // until the window is closed
}
```

- `new tk::Window(title)` opens a window; widgets are made with their
  parent as the first argument, and options in a map last.
- A widget shows once it is placed: `pack` puts widgets one under the
  other (or side by side with `{"side": "left"}`), `grid` puts them in
  rows and columns.
- `onclick` gives a button a function to call. The functions run in
  `events::run()`, the event loop of chapter 17, so timers, network
  connections and workers mix with the windows.
- Closing the window ends `events::run()`.

## A form

`grid` lines widgets up in rows and columns. An `Entry` is a field to
type in; `text()` reads it:

```
use tk;
use events;

main() {
    local w = new tk::Window("Sign up");
    local f = new tk::Frame(w, {"padding": 10});
    f->pack({"fill": "both", "expand": true});
    new tk::Label(f, "Name:")->grid({"row": 0, "column": 0, "sticky": "w"});
    local name = new tk::Entry(f, {"width": 24});
    name->grid({"row": 0, "column": 1, "sticky": "ew"});
    new tk::Label(f, "E-mail:")->grid({"row": 1, "column": 0, "sticky": "w"});
    local mail = new tk::Entry(f);
    mail->grid({"row": 1, "column": 1, "sticky": "ew"});
    local news = new tk::Checkbutton(f, "Send me news");
    news->grid({"row": 2, "column": 1, "sticky": "w"});
    f->columnconfigure(1, {"weight": 1});        // column 1 grows with the window
    local ok = new tk::Button(f, "Sign up");
    ok->grid({"row": 3, "column": 1, "sticky": "e", "pady": 5});
    ok->onclick(fn() {
        if (trim(name->text()) == "") {
            tk::messagebox("Please fill in your name.", {"icon": "warning", "parent": w});
            return;
        }
        println(name->text(), mail->text(), news->checked());
        w->close();
    });
    name->focus();
    events::run();
}
```

`sticky` says which sides of its cell a widget sticks to (`"w"`: left,
`"ew"`: stretched across); `columnconfigure` gives a column the extra
space when the window grows. `tk::messagebox` shows a message and waits
for its button. The other dialogs choose files: `tk::openfile()`,
`tk::savefile()`, `tk::choosedir()`.

## Menus and keys

```
use tk;
use events;

main() {
    local w = new tk::Window("Notes");
    local text = new tk::Text(w, {"width": 60, "height": 20, "scroll": "y"});
    text->pack({"fill": "both", "expand": true});
    local file = new tk::Menu(w)->submenu("File");
    file->item("Save as...", fn() {
        local path = tk::savefile({"ext": ".txt", "parent": w});

        if (path != nil) writefile(path, text->text());
    }, {"accelerator": "Ctrl+S"});
    file->separator();
    file->item("Quit", fn() { w->close(); });
    w->bind("<Control-Key-s>", fn(e) { file->invoke("Save as..."); });
    events::run();
}
```

- `new tk::Menu(w)` is the window's menu bar; `submenu` adds a menu to
  it, `item` an entry that calls a function.
- `w->bind(event, f)` calls `f(e)` for an event: a key (`"<Control-Key-s>"`,
  `"<Key-Return>"`), the mouse (`"<Button-1>"`, `"<B1-Motion>"`), and
  more. `e` is a map with the key, the mouse position and so on.
- A `Text` holds many lines; `"scroll": "y"` gives it a scroll bar.

[examples/notes.kkl](../../examples/notes.kkl) is a fuller notes window,
with opening and saving files.

## Drawing

A `Canvas` is a surface to draw lines, rectangles, ovals, text and
pictures on. Each item has an id, to move or change it later:

```
use tk;
use events;

main() {
    local w = new tk::Window("Drag the ball");
    local c = new tk::Canvas(w, {"width": 300, "height": 200, "background": "white"});
    c->pack();
    c->text(150, 20, "drag the ball with the mouse", {"color": "black"});
    local ball = c->oval(130, 80, 170, 120, {"fill": "#1a5fb4", "outline": "black"});
    local last = nil;
    c->binditem(ball, "<Button-1>", fn(e) { last = [e["x"], e["y"]]; });
    c->binditem(ball, "<B1-Motion>", fn(e) {
        c->move(ball, e["x"] - last[0], e["y"] - last[1]);
        last = [e["x"], e["y"]];
    });
    events::run();
}
```

`c->binditem(item, event, f)` binds an event to one item; `c->move`
moves it. With `events::every` (chapter 17) the same canvas becomes an
animation or a game.

## Readable colours

Leave the colours to the theme where you can: Tk's themed widgets look
like the rest of the desktop. Where you choose colours, as on a canvas,
dark text on a light background is easiest to read, and every pair of
colours should differ enough: a contrast ratio of at least 4.5 to 1 (the
WCAG AA rule) for text. Black on white is 21 to 1; the blue of the ball
above against white is about 6.3 to 1. Let users who need more contrast
have it, for example with a setting in a file.

## What there is

| Widgets | Other |
|---|---|
| `Window`, `Frame`, `Label`, `Button`, `Entry`, `Text`, `Checkbutton`, `Radiobutton` (with `Radiogroup`), `Listbox`, `Combobox`, `Scale`, `Canvas`, `Image` | `Menu` (menu bars and pop-up menus), `tk::messagebox`, `tk::openfile`, `tk::savefile`, `tk::choosedir`, themes (`tk::themes`, `tk::settheme`) |

The [reference](../reference.md#tk) describes each, with its options.

[The booklet](README.md) · previous: [20. The tools](20-tools.md) · next: Appendices (to come)
