# 7. Functions

[The booklet](README.md) · previous: [6. Statements](06-statements.md) · next: [8. Closures](08-closures.md)

A function gives a piece of a program a name, so that it can be used
again, tested on its own, and read as one idea. A Kukel function is
written at the top of a file: its name, its parameters, and its body.

## Defining and calling

```
area(w, h) {
    return w * h;
}

describe(w, h) {
    println(
        w,
        "by",
        h,
        "is",
        area(w, h),
    );
}

main() {
    describe(3, 4);
    local r = describe(1, 1);       // describe returns nothing: nil
    println(r);
}
```

```
$ kukel run area.kkl
3 by 4 is 12
1 by 1 is 1
nil
```

- There is no keyword before a function: `name(parameters) { body }`.
- `return value;` ends the function and gives the value. A function that
  ends without `return`, or with `return;`, gives `nil`.
- Functions may be defined in any order: `describe` can call `area`
  whether it comes before or after.
- Calling a function with the wrong number of arguments is an error when
  the program is compiled, not when it runs: a call `area(3)` anywhere in
  the program gets `area expects 2 arguments, got 1`, with its file, line
  and column.

## Defaults and the rest

A parameter may have a default value, used when the argument is left out.
After the defaults, one last parameter written `name..` collects the
remaining arguments in a vector:

```
greet(name, greeting = "hello", mark = greeting == "hello" ? "." : "!") {
    return greeting + ", " + name + mark;
}

total(first, rest..) {
    local sum = first;

    for (local x in rest) {
        sum += x;
    }
    return sum;
}

main() {
    println(greet("Ann"), greet("Bo", "welcome"));
    println(total(1), total(1, 2, 3));

    local numbers = [4, 5, 6];
    println(total(numbers..), total(0, numbers.., 10));
}
```

```
$ kukel run params.kkl
hello, Ann. welcome, Bo!
1 6
15 25
```

- A default may use the parameters before it, as `mark` uses `greeting`.
- An argument given as `nil` stays `nil`: the default is only for an
  argument that is left out.
- `numbers..` in a call does the opposite of `rest..`: it spreads a vector
  into separate arguments.

## Arguments by name

An argument can also be given by the name of its parameter, as
`name: value`, after the arguments given by place. That makes a call with
many arguments easier to read, and lets it skip an optional parameter in
the middle:

```
label(text, size = 12, bold = false, colour = "black") {
    return text + " (" + str(size) + (bold ? ", bold" : "") + ", " + colour + ")";
}

main() {
    println(label("title", bold: true));
    println(label("note", colour: "red", size: 9));
    println(label(colour: "blue", text: "link"));
}
```

```
$ kukel run named.kkl
title (12, bold, black)
note (9, red)
link (12, blue)
```

- The parameters that are not named keep their defaults: `size` in the
  first call, `bold` in the others.
- The named arguments may come in any order, but after those given by
  place; the arguments are computed in the order they are written.
- `kukel` checks the names when it compiles: a name the function has no
  parameter for, a name given twice, or a required parameter left out is
  an error before the program runs.
- Named arguments work in calls of functions, of static member functions
  and of constructors (`new Point(y: 2, x: 1)`, chapter 9), whose
  parameters `kukel` knows when it compiles. Built-in functions, and member
  functions called on an object (`p->m(...)`), take arguments by place
  only.

## Recursion

A function may call itself. Each call has its own parameters and locals:

```
factorial(n) {
    return n <= 1 ? 1 : n * factorial(n - 1);
}

// The ways to climb n stairs taking 1 or 2 at a time.
ways(n, memo = {}) {
    if (n <= 1) {
        return 1;
    }

    if (!haskey(memo, n)) {
        memo[n] = ways(n - 1, memo) + ways(n - 2, memo);
    }
    return memo[n];
}

main() {
    println(factorial(5), factorial(20));
    println(ways(10), ways(80));
}
```

```
$ kukel run recursion.kkl
120 2432902008176640000
89 37889062373143906
```

`ways` remembers what it computed in a map, so each `n` is worked out
once; without `memo`, `ways(80)` would take longer than you would like to
wait. Note that `memo = {}` makes a *new* map on every call that leaves it
out, unlike in Python: here the first call makes one, and passes it on.

Calls nest at most 10000 deep: a function that never stops calling itself
ends with a `StackOverflowError` instead of running out of memory.

## `main`

A program starts at `main`. It has no parameters, or one: the vector of
the program's arguments (chapter 1). What `main` returns becomes the
program's exit code when it is a number, which tells a shell script
whether the program succeeded (0) or not:

```
main(args) {
    if (len(args) == 0) {
        eprintln("usage: count words...");
        return 2;
    }
    println(len(args), "words");
}
```

```
$ kukel run count.kkl -- a b c
3 words
```

`eprintln` writes to standard error, where error messages belong.
`exit(code)` ends the program at once, from anywhere.

## Built-in functions

`println`, `len`, `push`, `sort` and the others are functions too, built
into kukel: they are called the same way, and can be passed around as
values (next chapter). The [reference](../reference.md#10-built-in-functions)
lists them all; most were in chapters 2 and 3.

[The booklet](README.md) · previous: [6. Statements](06-statements.md) · next: [8. Closures](08-closures.md)
