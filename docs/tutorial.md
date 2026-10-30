# A tour of Kukel

This tour takes about fifteen minutes. It goes from a first program to
classes, exceptions, modules, events and workers, one short program at a
time. Every program here is run by the tests, so what it prints is what
you will see. The full rules are in [reference.md](reference.md); this tour
only shows the way in.

Contents:

1. [A first program](#1-a-first-program)
2. [Values and decisions](#2-values-and-decisions)
3. [Vectors and maps](#3-vectors-and-maps)
4. [Functions](#4-functions)
5. [Classes](#5-classes)
6. [Exceptions](#6-exceptions)
7. [Modules](#7-modules)
8. [Events](#8-events)
9. [Workers](#9-workers)
10. [The tools](#10-the-tools)

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
    println(item, "x", count, "=", total);
    println("as text: " + str(total) + " euro");
    println(7 / 2, 7 % 2, 7.0 / 2, int("42") + 1);

    if (total > 5) println("expensive");
    else println("cheap");
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
        if (haskey(counts, w)) counts[w]++;
        else counts[w] = 1;
    }
    println(counts);

    local common = keys(counts);
    sort(common, fn(a, b) { return counts[a] > counts[b]; });
    println("most common:", common[0], counts[common[0]]);
    println(len(words), "words,", len(counts), "different");
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

    for (local x in rest) sum += x;
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

    for (local x in v) push(doubled, double(x));
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
Functions are values: they can be stored, passed and returned. A function
literal keeps the variables around it alive, so each counter that
`counter()` makes has its own `n`.

A function with `yield` in it is a *generator* function: calling it gives a
generator, which runs the body a step at a time, up to each `yield`. A
`for` loop takes its values one by one:

```
evens(limit) {
    for (local n = 0; n <= limit; n += 2) yield n;
}

main() {
    local v = [];

    for (local n in evens(6)) push(v, n);
    println(v);

    local g = evens(2);
    println(g->next(), g->next(), g->next(), g->done());
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
yields its values ([reference.md §7](reference.md#generators)).

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
Account::describe() { return owner + ": " + str(balance); }

class Savings : Account { rate; Savings(owner, rate); addinterest(); describe(); }
Savings::Savings(owner, rate) {
    Account::Account(owner);
    this->rate = rate;
}
Savings::addinterest() { deposit(balance * rate); }
Savings::describe() { return Account::describe() + " (savings)"; }

main() {
    local a = new Account("Ann"), s = new Savings("Bo", 0.1);
    a->deposit(50)->deposit(25);
    s->deposit(200);
    s->addinterest();

    for (local acc in [a, s]) println(acc->describe());
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

## 6. Exceptions

```
class InputError : Error { line; InputError(msg, line); }
InputError::InputError(msg, line) {
    Error::Error(msg);
    this->line = line;
}

parse(lines) {
    local numbers = [];

    for (local i = 0; i < len(lines); i++) {
        try {
            push(numbers, int(lines[i]));
        } catch (e) {
            throw new InputError("not a number: " + lines[i], i + 1);
        }
    }
    return numbers;
}

main() {
    println(parse(["1", "2", "3"]));

    try {
        parse(["4", "five", "6"]);
    } catch (e) {
        println(e->kind + " on line " + str(e->line) + ": " + e->message);
    } finally {
        println("done reading");
    }

    try {
        local v = [1, 2];
        println(v[5]);
    } catch (e) {
        println("caught", e->kind);
    }
}
```

```
$ kukel run errors.kkl
[1, 2, 3]
InputError on line 2: not a number: five
done reading
caught RangeError
```

`throw` throws an object of the class `Error`, or of a class derived from
it; `catch` catches it, and `finally` runs either way. Every error has a
`kind` (the name of its class, unless given) and a `message`. Errors of the
language itself are `Error` objects too, with kinds such as `TypeError`,
`RangeError` and `ValueError`, so the same `catch` handles them. An error
nothing catches ends the program with its kind, message and the calls that
led to it.

## 7. Modules

A program can be split into files. A *module* is a file that another file
uses:

```
// stats.kkl
mean(v) { return _sum(v) / float(len(v)); }

spread(v) { return max(v..) - min(v..); }

_sum(v) {
    local total = 0;

    for (local x in v) total += x;
    return total;
}
```

```
use json;
use stats;

main() {
    local text = "{\"city\": \"Utrecht\", \"temps\": [12, 15, 9, 14]}";
    local data = json::decode(text);
    local temps = data["temps"];
    println(data["city"], "mean", stats::mean(temps), "spread", stats::spread(temps));
    println(json::encode({"city": data["city"], "mean": stats::mean(temps)}));
}
```

```
$ kukel run weather.kkl
Utrecht mean 12.5 spread 6
{"city":"Utrecht","mean":12.5}
```

`use stats;` loads `stats.kkl` from the same directory (or from the
directories in `KUKEL_PATH`), and its names are reached as `stats::mean`.
A name that starts with `_` stays private to its module. `use json;` loads
a module written in Go that comes with kukel: there are modules for
databases (`db`), files (`fs`), JSON, regular expressions (`re`), dates
(`time`), text by character (`utf8`: `utf8::len("é")` is 1 where `len`
counts 2 bytes), UUIDs, networking (`zmq`, `p9`), windows (`tk`), and the two
below. Each is described in [reference.md §11](reference.md#11-modules-written-in-go).
One standard module is written in Kukel itself: `iter`, with `range`,
`map`, `filter`, `take`, `zip`, `reduce` and more, all generators
([reference.md §12](reference.md#12-modules-written-in-kukel)).

`fs` also opens files, to read or write them a line or a piece at a time
instead of whole; `with` closes them however its statement is left:

```
use fs;

main() {
    with (local out = fs::open("shopping.txt", "w")) {
        out->println("bread");
        out->println("cheese", 2);
    }

    with (local f = fs::open("shopping.txt")) {
        for (local i, line in f) println(str(i + 1) + ".", line);
    }
}
```

```
$ kukel run shopping.kkl
1. bread
2. cheese 2
```

`for` walks the lines of a file, as it walks the values of a generator
(with two names, a count from 0 too). `fs::open(path, "a")` appends, and `fs::stdin()`, `fs::stdout()` and
`fs::stderr()` are the standard streams as such files.

## 8. Events

A program that waits for several things at once, such as timers, network
messages or clicks in a window, gives each a function to call and lets the
event loop run:

```
use events;

main() {
    local n = 0, tick;
    tick = events::every(0.1, fn() {
        n++;
        println("tick", n);

        if (n == 3) tick->cancel();
    });
    events::after(0.25, fn() { println("a quarter second"); });
    println("start");
    events::run();
    println("nothing left to do");
}
```

```
$ kukel run timers.kkl
start
tick 1
tick 2
a quarter second
tick 3
nothing left to do
```

`events::every` calls its function every 0.1 seconds, `events::after`
once. `events::run()` calls them as their time comes, one at a time, and
returns when nothing is left to wait for. Sockets of `zmq` and windows of
`tk` deliver their events through the same loop, so one program can mix
them.

## 9. Workers

Workers run functions in parallel, each in an interpreter of its own:

```
use worker;

primes(from, to) {
    local count = 0;

    for (local n = from; n < to; n++) {
        local prime = n > 1;

        for (local d = 2; d * d <= n; d++) {
            if (n % d == 0) {
                prime = false;
                break;
            }
        }

        if (prime) count++;
    }
    return count;
}

main() {
    local workers = [];

    for (local i = 0; i < 4; i++) push(workers, worker::start(primes, i * 25000, (i + 1) * 25000));
    local total = 0;

    for (local w in workers) total += w->wait();
    println("primes below 100000:", total);
}
```

```
$ kukel run primes.kkl
primes below 100000: 9592
```

`worker::start(f, args...)` runs `f` in a new interpreter on its own
thread, and `w->wait()` gives what it returned. Workers share no
variables: they get copies of their arguments, and talk with `send` and
`receive` (or `onmessage`, through the event loop), so nothing needs a
lock.

## 10. The tools

`kukel` has more than `run`:

| Command | For |
|---|---|
| `kukel repl` | trying things out, a line at a time |
| `kukel fmt -w a.kkl` | laying out a file as all the examples here are |
| `kukel check a.kkl` | finding errors without running the program |
| `kukel debug a.kkl` | stopping at a line and looking at the values ([reference.md](reference.md#the-debugger)); in an editor with `kukel dap` ([debugging.md](debugging.md)) |
| `kukel profile a.kkl` | finding which functions and lines take the time ([reference.md](reference.md#the-profiler)) |
| `kukel build a.kkl` | compiling to a `.kkc` file that `kukel exec` runs |

Where to go next:

- [reference.md](reference.md): the whole language, and every module;
- [examples/](../examples/README.md): longer programs, such as a `grep`, a
  chat over ZeroMQ, a to-do list in SQLite and a notes window in Tk;
- [embedding.md](embedding.md): running Kukel from a Go program.
