# 8. Closures

[The booklet](README.md) · previous: [7. Functions](07-functions.md) · next: [9. Classes and member functions](09-classes.md)

Functions are values, as numbers and strings are: they can be stored in
variables, put in vectors and maps, passed to other functions and
returned from them. A *function literal*, written where it is needed,
makes this easy, and it remembers the variables around it: it is a
*closure*.

## Functions as values

```
double(x) { return 2 * x; }

apply(f, v) {
    local result = [];

    for (local x in v) push(result, f(x));
    return result;
}

main() {
    local f = double;                    // no (): the function itself
    println(f(21), apply(double, [1, 2, 3]));
    println(apply(fn(x) { return x * x; }, [1, 2, 3]));
    println(apply(str, [1, 2.5, nil]), apply(len, ["a", "bcd"]));

    local ops = {"+": fn(a, b) { return a + b; }, "-": fn(a, b) { return a - b; }};
    println(ops["+"](2, 3), ops["-"](2, 3));
}
```

```
$ kukel run values.kkl
42 [2, 4, 6]
[1, 4, 9]
["1", "2.5", "nil"] [1, 3]
5 -1
```

- A function's name without parentheses is the function itself.
- `fn(parameters) { body }` is a function literal: a function without a
  name, made where it stands. It takes defaults and `rest..` as a named
  function does.
- Built-in functions (`str`, `len`) are values too.

Sorting with your own order is the most common use (chapter 3):
`sort(people, fn(a, b) { return a["age"] < b["age"]; })`.

## Remembering variables

A function literal can use the variables of the function around it, and
keeps them alive as long as it needs them, even after that function has
returned:

```
counter(start) {
    local n = start;
    return fn() { return n++; };
}

main() {
    local a = counter(1), b = counter(100);
    println(a(), a(), a(), b(), a());
}
```

```
$ kukel run counter.kkl
1 2 3 100 4
```

Each call of `counter` makes a new `n`, so `a` and `b` count apart. The
variables are shared, not copied: when the closure changes `n`, the
function around it sees the change, and so does every other closure that
uses `n`.

```
main() {
    local total = 0;
    local add = fn(x) { total += x; };
    add(5);
    add(10);
    println(total);
}
```

```
$ kukel run shared.kkl
15
```

## Closures in loops

Every round of a loop has its own loop variable, so closures made in a
loop each remember their own:

```
main() {
    local buttons = [];

    for (local i = 1; i <= 3; i++) push(buttons, fn() { return "button " + str(i); });

    for (local b in buttons) println(b());
}
```

```
$ kukel run loop.kkl
button 1
button 2
button 3
```

(In JavaScript with `var`, or in Python, all three would say "button 3".)

## Functions that make functions

A function can take functions and give back a new one:

```
compose(f, g) {
    return fn(x) { return f(g(x)); };
}

adder(n) {
    return fn(x) { return x + n; };
}

main() {
    local addTen = adder(10);
    local twiceThenTen = compose(addTen, fn(x) { return 2 * x; });
    println(addTen(1), twiceThenTen(5));
}
```

```
$ kukel run compose.kkl
11 20
```

This is how event handlers, callbacks and generators (chapter 11) are
written: a small function that carries what it needs with it.

## Bound methods

An object's member function, taken without calling it, is a *bound
method*: a function value that calls the member function on that object
later (classes are the next chapter):

```
class Greeter {
    greeting;
    Greeter(greeting);
    greet(name);
}

Greeter::Greeter(greeting) { this->greeting = greeting; }
Greeter::greet(name) { return greeting + ", " + name; }

main() {
    local hi = new Greeter("hi");
    local f = hi->greet;               // no (): bound to hi
    println(f("Ann"));
    println(join([hi->greet("Bo"), f("Cy")], " / "));
}
```

```
$ kukel run bound.kkl
hi, Ann
hi, Bo / hi, Cy
```

[The booklet](README.md) · previous: [7. Functions](07-functions.md) · next: [9. Classes and member functions](09-classes.md)
