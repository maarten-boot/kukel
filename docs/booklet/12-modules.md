# 12. Your own modules

[The booklet](README.md) · previous: [11. Generators](11-generators.md) · next: [13. Files and directories](13-files.md)

A program can grow beyond one file. A *module* is a file that other files
use: its functions, classes, constants and globals are reached with the
module's name in front. Each module has its own names, so two modules can
both have a function `parse` without getting in each other's way.

## A module and its use

A file `money.kkl`:

```
// money.kkl
// Amounts of money in cents, so that no cent gets lost.
const CURRENCY = "EUR";

global rounding = 0;                    // how often a cent was rounded

class Amount {
    cents;
    Amount(cents);
    add(other);
    show();
}

Amount::Amount(cents) {
    this->cents = cents;
}
Amount::add(other) {
    return new Amount(cents + other->cents);
}
Amount::show() {
    return str(cents / 100) + "." + _two(cents % 100) + " " + CURRENCY;
}

parse(text) {
    local f = float(text) * 100;

    if (f != round(f)) {
        rounding++;
    }
    return new Amount(round(f));
}

_two(n) { // private to the module
    return n < 10 ? "0" + str(n) : str(n);
}
```

and a program next to it that uses it:

```
use money;

main() {
    local total = new money::Amount(0);

    for (local text in ["12.50", "0.99", "3.005"]) {
        total = total->add(money::parse(text));
    }
    println(total->show(), money::CURRENCY, money::rounding);
    println(total);
}
```

```
$ kukel run shop.kkl
16.50 EUR EUR 1
money::Amount{cents: 1650}
```

- `use money;` at the top of a file, before anything else, loads the
  module `money`: the file `money.kkl` in the same directory as the file
  that uses it.
- The module's names are reached as `money::parse`, `money::CURRENCY`,
  `money::rounding` (a global, which can be assigned too), and
  `new money::Amount(0)` for its class. A class of another module can be
  a base class: `class Price : money::Amount { ... }`.
- A name that starts with `_` is private: `money::_two` is an error
  outside `money.kkl`. The members of a module's classes are all public.
- A module sees its own names, the built-ins, and the modules it uses
  itself; not the names of the program that uses it.
- A module is loaded once, however many files use it. Its globals get
  their values before those of the files that use it.

## Where modules are found

For `use name;`, kukel looks for:

1. a *standard module* called `name`: one that comes with kukel
   (`kukel version` lists them, Part II);
2. the file `name.kkl` in the directory of the file with the `use`;
3. `name.kkl` in each directory of the environment variable
   `KUKEL_PATH` (separated by `:`, or `;` on Windows), in order.

So a library of your own modules can live in one directory: set
`KUKEL_PATH` to it, and every program can use them. When a module is not
found, the message lists where kukel looked.

## A module as one compiled file

`kukel build -module money.kkl` compiles a module on its own into
`money.kkc`. A program can then use `money` without the source file:
handy for a module you hand to others. `kukel build` of a program puts
the program and all its modules in one `.kkc` file, which `kukel exec`
runs.

## A module written in Kukel: `iter`

Kukel's own module `iter` (chapter 11) is written in Kukel, as a module of
yours would be. Its source,
[internal/stdlib/iter.kkl](../../internal/stdlib/iter.kkl), is a good
example to read: public functions that check their arguments, and private
generator functions (`_range`, `_map`, ...) that do the work.

```
use iter;

main() {
    for (local pair in iter::enumerate(["one", "two", "three"], 1)) {
        println(pair[0], pair[1]);
    }
    println(iter::first(iter::filter(fn(n) { return n * n > 50; }, iter::count())));
}
```

```
$ kukel run first.kkl
1 one
2 two
3 three
8
```

## Modules written in Go

The other standard modules are written in Go and built into kukel: files,
databases, the network, windows and more. A program uses them in the same
way, `use fs;` and then `fs::open(...)`. They are Part II of this
booklet. A Go program that embeds Kukel can add modules of its own in Go
(an appendix).

[The booklet](README.md) · previous: [11. Generators](11-generators.md) · next: [13. Files and directories](13-files.md)
