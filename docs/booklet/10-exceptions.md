# 10. Exceptions

[The booklet](README.md) · previous: [9. Classes and member functions](09-classes.md) · next: [11. Generators](11-generators.md)

Things go wrong while a program runs: a file is missing, a text is not a
number, an index is too big. Kukel then *throws an exception*: it stops
what it was doing and goes back through the calls until a `try` catches
it. A program can throw its own exceptions in the same way.

## Catching

```
parse(text) {
    try {
        return int(text);
    } catch (e) {
        println("not a number:", text, "(" + e->kind + ": " + e->message + ")");
        return 0;
    }
}

main() {
    println(parse("42") + parse("forty") + parse("8"));

    try {
        local v = [1, 2, 3];
        println(v[10]);
        println("never printed");
    } catch (e) {
        println("caught a", e->kind);
    }
}
```

```
$ kukel run catch.kkl
not a number: forty (ValueError: int: "forty" is not an integer)
50
caught a RangeError
```

- The statements in `try { ... }` run until one throws. The rest of the
  block is skipped, and the `catch` block runs with the exception in its
  variable.
- An exception is an object of the class `Error`, with the data members
  `kind` (what sort of error, such as `"ValueError"`), `message` (what
  went wrong) and `trace` (the calls it came through).
- `catch (e)` catches every exception. To handle only some, look at
  `e->kind` (or the class, below) and throw the others on.

## `finally`

A `finally` block runs however its `try` block ends: normally, by
`return` or `break`, or by an exception, caught or not:

```
work(fail) {
    try {
        println("working");

        if (fail) {
            throw new Error("it broke");
        }
        return "done";
    } finally {
        println("cleaning up");
    }
}

main() {
    println(work(false));

    try {
        work(true);
    } catch (e) {
        println("caught:", e->message);
    }
}
```

```
$ kukel run finally.kkl
working
cleaning up
done
working
cleaning up
caught: it broke
```

For something that has a `close()`, such as a file, `with` (chapter 6)
does this for you.

## Throwing

`throw` throws an `Error` object. `new Error(message, kind)` makes one;
without a kind it is `"Error"`. A class derived from `Error` can carry
more, and its name becomes the kind:

```
class InputError : Error {
    line;
    InputError(message, line);
}

InputError::InputError(message, line) {
    Error::Error(message);
    this->line = line;
}

readNumbers(lines) {
    local numbers = [];

    for (local i, text in lines) {
        try {
            push(numbers, int(text));
        } catch (e) {
            throw new InputError("not a number: " + text, i + 1);
        }
    }
    return numbers;
}

main() {
    println(readNumbers(["1", "2"]));

    try {
        readNumbers(["3", "four"]);
    } catch (e) {
        if (!instanceof(e, InputError)) {
            throw e;      // not ours: on it goes
        }
        println(
            e->kind,
            "on line",
            str(e->line) + ":",
            e->message,
        );
    }

    try {
        throw new Error("the disk is full", "IOError");
    } catch (e) {
        println(e->kind, e->message);
    }
}
```

```
$ kukel run throw.kkl
[1, 2]
InputError on line 2: not a number: four
IOError the disk is full
```

`throw e;` in a `catch` block throws the exception on, with the trace it
already has. Only `Error` objects can be thrown: `throw "oops"` is an
error.

## When nothing catches it

An exception that no `try` catches ends the program, with exit code 1. It
prints the kind, the message and the calls that led to it, the innermost
first. A file `average.kkl` whose `main` (on line 9) asks for the average
of an empty vector:

```
average(v) {
    local sum = 0;

    for (local x in v) {
        sum += x;
    }
    return sum / len(v);
}
```

```
$ kukel run average.kkl
ZeroDivisionError: integer division by zero
  at average (average.kkl:5)
  at main (average.kkl:9)
```

That is often the right thing: better to stop than to go on with wrong
data. Catch an exception where the program knows what to do about it.

## The kinds

The errors of the language and the modules come with these kinds, among
others:

| Kind | When |
|---|---|
| `TypeError` | an operation on a value of the wrong type: `1 + "a"`, calling a number |
| `ValueError` | a value a function cannot work with: `int("x")`, invalid JSON |
| `RangeError` | an index out of range, a number out of what a function takes |
| `ZeroDivisionError` | an int divided by the int 0 |
| `ArgumentError` | a wrong number of arguments, where kukel can only see it when it runs |
| `MemberError` | a member an object does not have |
| `IOError` | a file that cannot be read or written |
| `NetError` | a network problem (Part II) |
| `StackOverflowError` | calls nested too deep |

The [reference](../reference.md#9-exceptions) has all of them. Two things
are not exceptions and cannot be caught: `exit(code)`, which ends the
program at once, and being stopped from outside (by a Go program that
runs Kukel, Appendix).

[The booklet](README.md) · previous: [9. Classes and member functions](09-classes.md) · next: [11. Generators](11-generators.md)
