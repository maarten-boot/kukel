# 20. The tools

[The booklet](README.md) · previous: [19. The terminal](19-terminal.md) · next: [21. Windows](21-windows.md)

`kukel` is one program with many commands. Chapter 1 showed `run`,
`check` and `repl`. This chapter shows the others: for laying out code,
finding bugs, finding what is slow, and shipping a program.

## Laying out: `kukel fmt`

`kukel fmt` lays out source files the standard way: indentation, spaces
around operators, a blank line before `if` and the loops. Every program in
this booklet is laid out by it.

```
$ kukel fmt -l *.kkl            # which files would change
$ kukel fmt -w *.kkl            # change them
$ kukel fmt -w -strict a.kkl    # also braces, and long argument lists one per line
$ kukel fmt -w -vertical a.kkl  # every list of 2 or more one per line
```

Without `-w` it writes the result to standard output; `kukel fmt -`
formats standard input, for editors. Comments stay where they are.

`-strict` goes further. It adds the braces an `if`, `for`, `while`, ...
lacks, puts each statement of a function on a line of its own, and puts
the arguments of a call one per line when there are more than 3, or when
the line is longer than 120 characters (`-linelength 100` sets another
length). Take this function:

```
report(v, title) {
    println(title + ":", max(v..), min(v..), join(v, ", "));
    println(frame(title, 20, "-", true), len(v));
}
```

`kukel fmt -strict` makes it:

```
report(v, title) {
    println(
        title + ":",
        max(v..),
        min(v..),
        join(v, ", "),
    );
    println(
        frame(
            title,
            20,
            "-",
            true,
        ),
        len(v),
    );
}
```

Each argument starts a line of its own, one level in; the last one gets a
comma too, so that adding or moving a line never touches another; the `)`
lines up with the start of the statement. A call that holds such a list
among its arguments is split as well (the first `println` has four
arguments; the second holds `frame(...)`, a function of the program).
`kukel fmt -vertical` does the same for every call and definition with two
or more items, `report(v, title)` and `join(v, ", ")` included.

## Finding bugs: `kukel debug`

`kukel debug` runs a program and stops before its first line, where it
reads commands:

```
$ kukel debug shop.kkl
main at shop.kkl:12
   12      local cart = new Cart();
(kdb) b Cart::add
breakpoint 1 at shop.kkl:6
(kdb) c
breakpoint 1, Cart::add at shop.kkl:6
    6      items[name] = price;
(kdb) p name
"pen"
(kdb) bt
*0 Cart::add (shop.kkl:6)
 1 main (shop.kkl:13)
(kdb) c
1.5
the program ended (exit code 0)
```

| Command | Does |
|---|---|
| `b 12`, `b shop.kkl:12`, `b Cart::add`, `b 12 if i == 3` | stop at a line, or where a function starts; with `if`, only when the condition holds |
| `c` | run on to the next breakpoint |
| `s`, `n`, `f` | step to the next line, into calls (`step`) or over them (`next`); finish the function |
| `p expression` | show a value: `p v[i] + 1`, `p this->items` |
| `locals`, `bt`, `frame n`, `l` | the variables; the calls; look at another call; the source around |
| `q` | quit |

An exception that nothing catches stops the program where it is thrown,
so you can look around before it ends.

**In an editor.** `kukel dap` is the same debugger for editors, through
the Debug Adapter Protocol: Visual Studio Code (with the extension in
`editors/vscode`), Neovim, Helix and Emacs. Breakpoints are set by
clicking in the margin, and values show as the mouse passes over them.
[debugging.md](../debugging.md) explains the setup for each editor;
`editors/geany` gives Geany colours and a Build menu.

## Finding what is slow: `kukel profile`

`kukel profile` runs a program and then reports where its time went, by
function and by line:

```
$ kukel profile primes.kkl
17984 primes
0.430 s in 313 samples, one every 1.38 ms on average (wall time, with waiting).
...
Functions, by their own time (total: with the functions they call):
        own      %      total      %       calls  function
    0.313 s  72.7%    0.313 s  72.7%      200000  isPrime (primes.kkl:4)
    0.118 s  27.3%    0.430 s 100.0%           1  main (primes.kkl:10)

Lines, by their own time (the 4 slowest):
        own      %  line
    0.313 s  72.7%  primes.kkl:5  for (local d = 2; d * d <= n; d++)
...
```

Measure before you change anything: the slow part is rarely where you
expect it. `-cpu` leaves waiting out (for a network or a timer);
`-pprof file` also writes the profile for Go's `go tool pprof`, which
draws flame graphs.

## Shipping: `kukel build` and `kukel exec`

`kukel build` compiles a program and all its modules into one bytecode
file, which `kukel exec` runs without the sources:

```
$ kukel build -o shop.kkc shop.kkl
$ kukel exec shop.kkc
1.5
```

A `.kkc` file starts faster (nothing to compile) and needs only kukel
itself. It belongs to its kukel's *bytecode format*: after an upgrade
that changes the format (the CHANGELOG says so), build it again from the
source. Source is what keeps working (see
[compatibility](../compatibility.md)).

## Looking inside

For the curious, and for those who work on kukel itself:

| Command | Shows |
|---|---|
| `kukel tokens a.kkl` | the words and symbols the compiler sees |
| `kukel ast a.kkl` | the syntax tree |
| `kukel disasm a.kkl` | the bytecode, the instructions of the virtual machine ([vm.md](../vm.md)) |
| `kukel version` | the version, and the modules this kukel has |

[The booklet](README.md) · previous: [19. The terminal](19-terminal.md) · next: [21. Windows](21-windows.md)
