# 19. The terminal

[The booklet](README.md) · previous: [18. Networking](18-networking.md) · next: [20. The tools](20-tools.md)

Most programs talk to people with `println` and `readline()`: a line at a
time, after Enter. A program that reacts to every key, such as a menu, a
game or an editor, needs the terminal a key at a time. That is the module
`term`.

## A key at a time

```
use term;

main() {
    println(
        "a terminal:",
        term::isterm(),
    );
    local moves = {"Up": [0, -1], "Down": [0, 1], "Left": [-1, 0], "Right": [1, 0]};
    local x = 0, y = 0;

    term::raw(fn() {
        print("arrows move, q quits\r\n");

        for (local k = term::key(); k != nil && k != "q"; k = term::key()) {
            if (haskey(
                moves,
                k,
            )) {
                x += moves[k][0];
                y += moves[k][1];
            }
            print(
                "key",
                k,
                "-> position",
                x,
                str(y) + "\r\n",
            );
        }
    });
    println(
        "ended at",
        x,
        y,
    );
}
```

Run in a terminal, it shows each key as it is pressed:

```text
a terminal: true
arrows move, q quits
key Up -> position 0 -1
key Right -> position 1 -1
key a -> position 1 -1
ended at 1 -1
```

- `term::raw(f)` calls `f` with the terminal in *raw mode*: keys come at
  once, without Enter, and are not shown. Afterwards the terminal is put
  back as it was, also when `f` throws an exception.
- `term::key()` waits for the next key and gives it as a string: the
  character typed (`"a"`, `"é"`), or a name such as `"Enter"`, `"Esc"`,
  `"Up"`, `"PageDown"`, `"F5"`, `"Ctrl-S"`, `"Shift-Tab"`. At the end of
  the input it gives `nil`. `term::key(0.5)` gives `nil` when no key comes
  within half a second, for a game that moves on by itself.
- In raw mode a new line needs `"\r\n"`: `"\n"` alone moves down without
  going back to the left edge.
- `term::size()` is `[columns, rows]`; `term::isterm()` says whether there
  is a terminal at all.

## Drawing the screen

A full-screen program draws with *ANSI escape codes*, strings that start
with the Esc character, `"\x1b"`, and that every terminal understands:

| Code | Does |
|---|---|
| `"\x1b[2J"` | clears the screen |
| `"\x1b[H"`, `"\x1b[5;10H"` | puts the cursor at the top left, or at row 5, column 10 |
| `"\x1b[K"` | clears to the end of the line |
| `"\x1b[7m"`, `"\x1b[1m"`, `"\x1b[0m"` | reversed, bold, normal text |
| `"\x1b[?25l"`, `"\x1b[?25h"` | hides and shows the cursor |

[examples/vi.kkl](../../examples/vi.kkl) is a small vi in about 700 lines
of Kukel built on just these: a good program to read.

## Testing with keys from a file

Keys come from standard input, which need not be a terminal. From a pipe
or a file the same bytes give the same keys: a new line is `"Enter"`, the
byte `\x1b` is Esc, `\x1b[A` is Up. So a program that reads keys can be
tested without a person at the keyboard:

```
$ printf '\x1b[A\x1b[Cq' | kukel run arrows.kkl
a terminal: false
arrows move, q quits
key Up -> position 0 -1
key Right -> position 1 -1
ended at 1 -1
```

Without a terminal, `raw` changes nothing, and `size()` is what the
environment variables `COLUMNS` and `LINES` say, else `[80, 24]`. This is how
the tests of `examples/vi.kkl` drive the editor.

[The booklet](README.md) · previous: [18. Networking](18-networking.md) · next: [20. The tools](20-tools.md)
