# 11. Generators

[The booklet](README.md) · previous: [10. Exceptions](10-exceptions.md) · next: [12. Your own modules](12-modules.md)

A *generator* produces values one at a time, when they are asked for. It
is written as an ordinary function with `yield` in it, and a `for` loop
takes its values as it takes the elements of a vector. A generator can
walk a tree, read a file line by line, or count forever, without building
a vector of everything first.

## `yield`

```
countdown(n) {
    for (local i = n; i > 0; i--) {
        yield i;
    }
    println("liftoff");
}

main() {
    for (local x in countdown(3)) {
        println(x);
    }

    local g = countdown(2);
    println(g);
    println(g->next(), g->next(), g->done());
    println(g->next(), g->done());
}
```

```
$ kukel run countdown.kkl
3
2
1
liftoff
Generator(countdown)
2 1 false
liftoff
nil true
```

- A function with `yield` in it is a *generator function*. Calling it
  does not run its body: it gives a generator, an object of the built-in
  class `Generator`.
- Each value asked for runs the body up to the next `yield x;`, which
  gives `x` and pauses there, with all its locals, until the next value is
  asked for.
- `for` asks for values until the body ends. `g->next()` asks for one;
  after the end it gives `nil`, and `g->done()` says whether the end was
  reached. After two values `g` is not done yet: it is paused at its
  `yield`. Only the third `next()` runs on to the end of the body, and
  prints `liftoff`.

Because nothing runs until a value is asked for, a generator may go on
forever; the loop that uses it decides when to stop:

```
fibonacci() {
    local a = 0, b = 1;

    for (;;) {
        yield a;
        local next = a + b;
        a = b;
        b = next;
    }
}

main() {
    local first = [];

    for (local i, f in fibonacci()) {
        if (i == 10) {
            break;
        }
        push(first, f);
    }
    println(first);
}
```

```
$ kukel run fibonacci.kkl
[0, 1, 1, 2, 3, 5, 8, 13, 21, 34]
```

With two names, `for` also counts the values, from 0.

## A class that `for` can walk

A class becomes something `for` can walk by giving it a member function
`iter()` that yields its values. `yield from x;` yields all the values of
`x`, another generator or anything `for` walks:

```
class Tree {
    left, value, right;
    Tree(left, value, right);
    iter();
}

Tree::Tree(left, value, right) {
    this->left = left;
    this->value = value;
    this->right = right;
}

Tree::iter() {                          // in order: left, value, right
    if (left != nil) {
        yield from left;
    }
    yield value;

    if (right != nil) {
        yield from right;
    }
}

leaf(v) {
    return new Tree(nil, v, nil);
}

main() {
    local t = new Tree(new Tree(leaf(1), 2, leaf(3)), 4, leaf(5));

    for (local x in t) {
        print(x);
    }
    println();
}
```

```
$ kukel run tree.kkl
12345
```

The recursion is all inside `iter`: the loop in `main` just sees values.

## Sending values back

`yield` can also receive a value. `g->send(x)` runs the generator on, and
the `yield` it was paused at gives `x`:

```
average() {
    local total = 0, count = 0;

    for (;;) {
        local x = yield count == 0 ? 0 : total / float(count);
        total += x;
        count++;
    }
}

main() {
    local avg = average();
    avg->next();                         // run to the first yield
    println(avg->send(10), avg->send(20), avg->send(60));
}
```

```
$ kukel run average.kkl
10.0 15.0 30.0
```

The first `next()` runs the body up to its first `yield`, so that there is
a `yield` waiting for what `send` passes.

## Closing

A loop that stops before the generator's end (by `break`, `return` or an
exception) *closes* it: the body ends at its `yield`, as if a `return`
stood there, and its `finally` blocks run. So a generator that opens a
file closes it, also when the loop stops early:

```
numbers() {
    try {
        for (local i = 1;; i++) {
            yield i;
        }
    } finally {
        println("closed");
    }
}

main() {
    for (local n in numbers()) {
        if (n > 2) {
            break;
        }
        println(n);
    }
    println("after the loop");
}
```

```
$ kukel run closing.kkl
1
2
closed
after the loop
```

`g->close()` closes a generator by hand.

## The module `iter`

The standard module `iter` (written in Kukel itself) has generators that
make, change and combine sequences, and functions that use them up:

```
use iter;

main() {
    local squares = iter::map(fn(x) { return x * x; }, iter::count(1));
    local even = iter::filter(fn(x) { return x % 2 == 0; }, squares);
    println(iter::collect(iter::take(even, 4)));
    println(iter::reduce(fn(a, b) { return a + b; }, iter::range(1, 101)));
    println(iter::collect(iter::zip(["a", "b", "c"], iter::count())));
}
```

```
$ kukel run iter.kkl
[4, 16, 36, 64]
5050
[["a", 0], ["b", 1], ["c", 2]]
```

`iter::count(1)` counts on forever; `map` and `filter` change and choose
values as they pass; `take` stops after four; `collect` puts what is left
in a vector. Nothing is computed that is not needed. The functions are
`range`, `count`, `map`, `filter`, `take`, `drop`, `takewhile`,
`dropwhile`, `enumerate`, `zip`, `chain`, `collect`, `reduce`, `first`,
`any` and `all`; the [reference](../reference.md#iter) describes each.

[The booklet](README.md) · previous: [10. Exceptions](10-exceptions.md) · next: [12. Your own modules](12-modules.md)
