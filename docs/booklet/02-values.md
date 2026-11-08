# 2. Values and types

[The booklet](README.md) · previous: [1. Getting started](01-getting-started.md) · next: [3. Strings, vectors and maps](03-collections.md)

Kukel is *dynamically typed*: a variable has no type of its own, the value
in it does. The same variable can hold a number now and a string later.
This chapter goes through the kinds of values, which are true and which
false, and what `==` compares.

## The types

```
main() {
    println(
        typeof(nil),
        typeof(true),
        typeof(42),
        typeof(4.2),
        typeof("hi"),
    );
    println(
        typeof([1, 2]),
        typeof({"a": 1}),
        typeof(main),
        typeof(Error),
        typeof(new Error("x")),
    );
}
```

```
$ kukel run types.kkl
nil bool int float string
vector map function class object
```

| Type | Examples | What it is |
|---|---|---|
| nil | `nil` | "nothing": the value of a variable that was given none |
| bool | `true`, `false` | |
| int | `42`, `-7`, `0xFF`, `0b1010`, `1_000_000`, `'A'` | a whole number of 64 bits |
| float | `3.5`, `1e6`, `.5` | a number with a fraction (64-bit floating point) |
| string | `"hello"` | text: a row of bytes (chapter 3) |
| vector | `[1, "two", [3]]` | a list of values (chapter 3) |
| map | `{"name": "Ann", "age": 41}` | values found by a key (chapter 3) |
| function | `main`, `fn(x) { return 2 * x; }` | something to call (chapters 7 and 8) |
| class | `Error` | what objects are made from (chapter 9) |
| object | `new Error("x")` | an object of a class (chapter 9) |

`typeof(x)` gives the name of the type as a string.

## Numbers

Kukel has two kinds of numbers, ints and floats, and keeps them apart
where it matters:

```
main() {
    println(
        7 / 2,
        7 % 2,
        -7 / 2,
        7.0 / 2,
        1 / 3.0,
    );
    println(
        2.0 * 3,
        1e6,
        0.00001,
        0xFF,
        0b1010,
        1_000_000,
        'A',
    );
    println(9223372036854775807 + 1);
    println(1 / 0.0, -1 / 0.0, 0 / 0.0);
}
```

```
$ kukel run numbers.kkl
3 1 -3 3.5 0.3333333333333333
6.0 1e+06 1e-05 255 10 1000000 65
-9223372036854775808
inf -inf nan
```

- An int divided by an int is an int: the fraction is dropped (`7 / 2` is
  3, `-7 / 2` is -3). `%` is the remainder. As soon as a float takes part,
  the result is a float: `7.0 / 2` is 3.5.
- A float always prints with a `.` or an exponent, so you can see it is a
  float: `6.0`, not `6`.
- Ints wrap around: one past the largest int is the smallest. An int
  divided by the int 0 is an error (`ZeroDivisionError`); a float divided
  by zero gives `inf`, `-inf` or `nan` ("not a number").
- `'A'` is the code of a character, an int: 65. `_` may separate digits
  for readability.

## Converting

`+` adds numbers and joins strings, but it does not mix them: `"total " +
7` is an error (a `TypeError`). Convert first, with `str`, `int` and
`float`:

```
main() {
    local n = 7;
    println("total " + str(n));
    println(
        int("42") + 1,
        int(3.9),
        int(-3.9),
        int("0x10"),
        float(2),
        float("2.5"),
    );
}
```

```
$ kukel run convert.kkl
total 7
43 3 -3 16 2.0 2.5
```

`int` of a float drops the fraction (toward zero). `int` of a string that
is not a number is an error (a `ValueError`), which a program can catch
(chapter 10). `println` and `print` take any values, so `println("total",
n)` needs no conversion at all.

## True and false

`if`, `while` and the other statements that decide take any value.
`nil`, `false`, `0` and `0.0` count as false; everything else counts as
true, also the empty string, the empty vector and the empty map:

```
main() {
    local verdicts = [];

    for (local x in [nil, false, 0, 0.0, "", [], {}, "no", 1]) {
        if (x) {
            push(verdicts, "true");
        } else {
            push(verdicts, "false");
        }
    }
    println(join(verdicts, " "));
}
```

```
$ kukel run truth.kkl
false false false false true true true true true
```

`!x` turns a value into the opposite `bool`; `!!x` gives the `bool` it
counts as.

## Equal

`==` and `!=` compare:

- numbers by value, also an int with a float: `1 == 1.0` is true;
- strings by their text: `"a" == "a"`;
- `nil`, `true` and `false` by value;
- vectors, maps, objects, classes and functions by *identity*: two of
  them are equal only when they are the same one. `[1] == [1]` is false:
  those are two vectors that happen to hold the same. A member function
  taken from an object (`p->m`, chapter 8) equals the same member
  function taken from the same object.

Values of different types are never equal (but for ints and floats):
`nil == false` and `0 == "0"` are false.

## Shared, not copied

A vector, a map or an object lives once. A variable holds a reference to
it, so assigning it to another variable, or passing it to a function,
does not copy it: both see the same one.

```
main() {
    local a = [1, 2];
    local b = a;               // the same vector
    push(b, 3);
    println(a, b, a == b);

    local c = slice(a, 0);     // a copy
    push(c, 4);
    println(a, c, a == c);
}
```

```
$ kukel run shared.kkl
[1, 2, 3] [1, 2, 3] true
[1, 2, 3] [1, 2, 3, 4] false
```

This is how most languages with garbage collection work (Python, Java,
JavaScript). Numbers, strings, `nil` and booleans can never be changed, so
for them the question does not come up: `s + "x"` makes a new string.
There is no need to free anything: values no one can reach any more are
cleaned up on their own.

[The booklet](README.md) · previous: [1. Getting started](01-getting-started.md) · next: [3. Strings, vectors and maps](03-collections.md)
