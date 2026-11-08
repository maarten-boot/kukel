# 4. Variables and scope

[The booklet](README.md) · previous: [3. Strings, vectors and maps](03-collections.md) · next: [5. Expressions and operators](05-expressions.md)

Every variable is declared before it is used, with `local`, `global` or
`const`. Where it is declared decides where its name can be seen: its
*scope*. kukel checks all names when it compiles, so a misspelt name is an
error before the program runs, not a surprise halfway.

## Locals

`local` declares variables in the block around it, the nearest `{ ... }`.
The name can be used from its declaration to the end of that block:

```
main() {
    local x = 1, y;                 // y is nil
    println(x, y);

    {
        local x = 2;                // another x, in this block only
        println(x);
    }
    println(x);                     // the first x again

    for (local i = 0; i < 3; i++) {
        print(i);
    }
    println();                      // i is gone here
}
```

```
$ kukel run locals.kkl
1 nil
2
1
012
```

- A variable without a value is `nil`.
- An inner block may declare a name again; it hides the outer one until
  the block ends. Within one block a name is declared once.
- The variable of a `for` loop belongs to the loop.
- A `local` must stand directly in a block: `if (ok) local x = 1;` is an
  error (what would `x` be after the `if`?). Write `if (ok) { ... }`.

Parameters are locals too, of the whole function body.

## Globals

`global` declares variables at the top of a file, outside any function.
Every function of the program can use them, in any of its files, and also
the functions above the declaration:

```
global calls = 0, history = [];

record(what) {
    calls++;
    push(history, what);
}

main() {
    record("start");
    record("stop");
    println(calls, history);
}
```

```
$ kukel run globals.kkl
2 ["start", "stop"]
```

A global's value is computed before `main` starts, in the order of the
file. Assigning to a name that was never declared is an error, so a typo
cannot quietly make a new variable:

```
add(n) {
    total += n;
}
```

```
$ kukel check count.kkl
count.kkl:2:5: undefined name total (declare globals with 'global')
```

## Constants

`const` declares a name whose value never changes. Its value is computed
when the program is compiled, so it may use only literals, other
constants and operators, not calls:

```
const WIDTH = 80, HALF = WIDTH / 2;
const GREETING = "hello";

main() {
    println(WIDTH, HALF, GREETING + "!");
}
```

```
$ kukel run consts.kkl
80 40 hello!
```

Assigning to a constant is a compile error. Constants can be the labels
of a `switch` (chapter 6).

## One name, one meaning

The top-level names of a program, its globals, constants, functions and
classes, share one space with the built-in functions: each name is
defined once. A program cannot define its own `len`, and cannot assign to
`println`. A *local* may take a built-in's name, though, and then hides it
where it is visible:

```
main() {
    local len = 3;                  // hides the built-in len here
    println(len * 2);
}
```

```
$ kukel run hide.kkl
6
```

That is allowed, but rarely a good idea.

## Where a name is looked up

When kukel meets a name, it looks for it in this order:

1. the locals and parameters of the function, innermost block first;
2. the variables of the functions around it, for a function written inside
   another (chapter 8);
3. inside a member function, the members of the object (chapter 9);
4. the top-level names of the program, and the built-ins.

Modules have their own top-level names: a name of a module is written
with the module in front, as `stats::mean` (chapter 12).

[The booklet](README.md) · previous: [3. Strings, vectors and maps](03-collections.md) · next: [5. Expressions and operators](05-expressions.md)
