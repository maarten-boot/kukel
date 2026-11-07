# A tour of Kukel

This tour takes about ten minutes: a first program, values, vectors and
maps, functions and classes, one short program at a time. Every program
here is run by the tests, so what it prints is what you will see. Then
[the booklet](booklet/README.md) takes over: it teaches the whole
language and every module, chapter by chapter, and is also in each
release as a PDF. The full rules are in [reference.md](reference.md).

Contents:

1. [A first program](#1-a-first-program)
2. [Values and decisions](#2-values-and-decisions)
3. [Vectors and maps](#3-vectors-and-maps)
4. [Functions](#4-functions)
5. [Classes](#5-classes)
6. [Where to go next](#6-where-to-go-next)

## 1. A first program

Install kukel as the [README](../README.md#installing) says, and put this
in a file `hello.kkl`:

```
main(args) {
    local name = len(args) > 0 ? args[0] : "world";
    println("hello,", name);
}
```

```
$ kukel run hello.kkl -- Kukel
hello, Kukel
```

A program starts at `main`. `args` holds what follows `--` on the command
line (without it, `args` is empty and the program greets the world).
`local` declares a variable; every variable must be declared before it is
used, which catches typing errors before the program runs. `println`
writes its arguments separated by spaces, and a newline.

A file that starts with the line `#!/usr/bin/env kukel` and is executable
runs as a command of its own: `./hello.kkl Kukel`.

## 2. Values and decisions

```
main() {
    local count = 3, price = 2.5, item = "apple";
    local total = count * price;
    println(
        item,
        "x",
        count,
        "=",
        total,
    );
    println("as text: " + str(total) + " euro");
    println(
        7 / 2,
        7 % 2,
        7.0 / 2,
        int("42") + 1,
    );

    if (total > 5) {
        println("expensive");
    } else {
        println("cheap");
    }
}
```

```
$ kukel run values.kkl
apple x 3 = 7.5
as text: 7.5 euro
3 1 3.5 43
expensive
```

Kukel has ints, floats, strings, booleans (`true`, `false`) and `nil`.
Variables have no type of their own; values do. An int divided by an int
is an int (`7 / 2` is 3); with a float in it, the result is a float. `+`
joins strings, but does not turn a number into a string: `"total " + 7` is
a `TypeError`, so write `"total " + str(7)`, or give `println` several
arguments. `int`, `float` and `str` convert. `if`, `else`, `while`, `for`
and `switch` work as in C.

## 3. Vectors and maps

```
main() {
    local words = split("the cat saw the dog and the bird", " ");
    local counts = {};

    for (local w in words) {
        if (haskey(counts, w)) {
            counts[w]++;
        } else {
            counts[w] = 1;
        }
    }
    println(counts);

    local common = keys(counts);
    sort(common, fn(a, b) { return counts[a] > counts[b]; });
    println("most common:", common[0], counts[common[0]]);
    println(
        len(words),
        "words,",
        len(counts),
        "different",
    );
}
```

```
$ kukel run words.kkl
{"the": 3, "cat": 1, "saw": 1, "dog": 1, "and": 1, "bird": 1}
most common: the 3
8 words, 6 different
```

A vector is a list, `[1, 2, 3]`, indexed from 0. A map, `{"key": value}`,
keeps its keys in the order they were added. `for (local x in v)` goes
over the elements of a vector, or the keys of a map. `sort` takes a
function that says whether `a` goes before `b`; here it is a function
literal (`fn(a, b) { ... }`), written where it is needed. `push`, `pop`,
`slice`, `join`, `keys`, `values` and `delete` are some of the other
[built-in functions](reference.md#10-built-in-functions).

## 4. Functions

```
greet(name, greeting = "hello") {
    return greeting + ", " + name;
}

total(first, rest..) {
    local sum = first;

    for (local x in rest) {
        sum += x;
    }
    return sum;
}

counter() {
    local n = 0;
    return fn() { return ++n; };
}

main() {
    println(greet("Ann"), "/", greet("Bo", "hi"));
    println(total(1), total(1, 2, 3), total([4, 5]..));

    local next = counter();
    next();
    next();
    println("counted", next());

    local double = fn(x) { return 2 * x; };
    local v = [1, 2, 3];
    local doubled = [];

    for (local x in v) {
        push(doubled, double(x));
    }
    println(doubled);
}
```

```
$ kukel run functions.kkl
hello, Ann / hi, Bo
1 6 9
counted 3
[2, 4, 6]
```

A parameter can have a default (`greeting = "hello"`), and the last one can
collect the rest of the arguments in a vector (`rest..`). `v..` does the
opposite in a call: it passes the elements of `v` as separate arguments.
An argument can also be given by name, after the others:
`greet("Bo", greeting: "welcome")`.
Functions are values: they can be stored, passed and returned. A function
literal keeps the variables around it alive, so each counter that
`counter()` makes has its own `n`.

A function with `yield` in it is a *generator* function: calling it gives a
generator, which runs the body a step at a time, up to each `yield`. A
`for` loop takes its values one by one:

```
evens(limit) {
    for (local n = 0; n <= limit; n += 2) {
        yield n;
    }
}

main() {
    local v = [];

    for (local n in evens(6)) {
        push(v, n);
    }
    println(v);

    local g = evens(2);
    println(
        g->next(),
        g->next(),
        g->next(),
        g->done(),
    );
}
```

```
$ kukel run evens.kkl
[0, 2, 4, 6]
0 2 nil true
```

The generator keeps its place and its locals between steps, so a long or
endless series costs nothing until it is asked for. A class becomes
something `for` can walk by giving it a member function `iter()` that
yields its values; `yield from x;` yields all the values of another
generator (or of a vector), and `g->send(x)` hands a value back to the
generator, which gets it from `local x = yield v;`
([reference.md §7](reference.md#generators)).

## 5. Classes

```
class Account { owner; balance; Account(owner); deposit(amount); describe(); }
Account::Account(owner) {
    this->owner = owner;
    balance = 0;
}
Account::deposit(amount) {
    balance += amount;
    return this;
}
Account::describe() {
    return owner + ": " + str(balance);
}

class Savings : Account { rate; Savings(owner, rate); addinterest(); describe(); }
Savings::Savings(owner, rate) {
    Account::Account(owner);
    this->rate = rate;
}
Savings::addinterest() {
    deposit(balance * rate);
}
Savings::describe() {
    return Account::describe() + " (savings)";
}

main() {
    local a = new Account("Ann"), s = new Savings("Bo", 0.1);
    a->deposit(50)->deposit(25);
    s->deposit(200);
    s->addinterest();

    for (local acc in [a, s]) {
        println(acc->describe());
    }
    println(a);
}
```

```
$ kukel run accounts.kkl
Ann: 75
Bo: 220.0 (savings)
Account{owner: "Ann", balance: 75}
```

A class declares its data members and its member functions in braces; the
functions are written after it, as `Class::name(...) { ... }`. Inside them
the data members are reached by name (`balance`), or as `this->owner` where
a parameter has the same name. `new` makes an object and runs the
constructor; `->` reaches a member of an object. A class derives from at
most one other (`Savings : Account`); it calls the constructor it derives
from as `Account::Account(owner)`, and `Account::describe()` runs the
version it replaces. Every member function is virtual: `acc->describe()`
runs the one of the object's own class.

## 6. Where to go next

The [booklet](booklet/README.md) goes on from here:

| To learn about | Read |
|---|---|
| exceptions: `try`, `catch`, `throw` | [chapter 10](booklet/10-exceptions.md) |
| generators: `yield` | [chapter 11](booklet/11-generators.md) |
| splitting a program into modules | [chapter 12](booklet/12-modules.md) |
| files and directories | [chapter 13](booklet/13-files.md) |
| JSON, regular expressions, text by character | [chapter 14](booklet/14-text.md) |
| databases | [chapter 16](booklet/16-databases.md) |
| timers, events and workers in parallel | [chapter 17](booklet/17-events.md) |
| the network | [chapter 18](booklet/18-networking.md) |
| the debugger, the profiler and the other tools | [chapter 20](booklet/20-tools.md) |
| windows | [chapter 21](booklet/21-windows.md) |
| running Kukel from a Go program | [appendix A](booklet/22-embedding.md) |

And elsewhere:

- [reference.md](reference.md): the whole language, and every module, in
  full detail;
- [examples/](../examples/README.md): longer programs, such as a `grep`, a
  small `vi`, a chat over ZeroMQ, a to-do list in SQLite and a notes window
  in Tk.
