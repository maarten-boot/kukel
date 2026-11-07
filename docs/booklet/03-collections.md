# 3. Strings, vectors and maps

[The booklet](README.md) · previous: [2. Values and types](02-values.md) · next: [4. Variables and scope](04-scope.md)

Three kinds of values hold more than one thing: a string holds bytes, a
vector holds values in a row, a map holds values by key. Together with
the built-in functions on them they do most of the work in most programs.

## Strings

A string is written in double quotes and may span lines. A backslash
starts an *escape*: `\n` (a new line), `\t` (a tab), `\"`, `\\`, `\x41` (a
byte by its code in hexadecimal) and `\u{E9}` (a character by its Unicode
code: `"caf\u{E9}"` is `"café"`).

```
main() {
    local s = "Hello, World";
    println(
        len(s),
        s[0],
        char(s[0]),
        slice(s, 7),
        slice(s, 0, 5),
    );
    println(
        upper(s),
        lower(s),
        find(s, "World"),
        find(s, "x"),
    );
    println(split("a,b,,c", ","), join(["x", "y", "z"], "-"));
    println(replace(s, "World", "Kukel"), "[" + trim("  padded \n") + "]");
    println("tab\tthen a quote: \" and a backslash: \\");
}
```

```
$ kukel run strings.kkl
12 72 H World Hello
HELLO, WORLD hello, world 7 -1
["a", "b", "", "c"] x-y-z
Hello, Kukel [padded]
tab	then a quote: " and a backslash: \
```

- `len(s)` is the length in bytes. `s[i]` is the byte at `i` (from 0), as
  an int; `char(n)` makes it a string again.
- `slice(s, start, end)` is the part from `start` up to, not including,
  `end` (to the end when `end` is left out).
- `find` gives the index of the first match, or -1.
- `split` cuts a string at a separator; `join` glues a vector of values
  together.
- Strings cannot be changed: `upper`, `replace`, `trim` and `+` give new
  ones.
- Strings compare with `==`, `<` and the rest by their bytes, so `"Z" <
  "a"`.

Text in UTF-8 passes through unchanged: `println("café")` prints café.
But `len("café")` is 5: the é takes two bytes. The module `utf8` (Part II)
counts and cuts by character.

## Vectors

A vector is a row of values of any type, indexed from 0. It grows and
shrinks:

```
main() {
    local v = [3, 1, 2];
    push(v, 10, 20);            // at the end
    println(
        v,
        len(v),
        v[0],
        v[len(v) - 1],
    );
    println(pop(v), v);         // from the end
    insert(v, 0, 99);           // at index 0
    println(v);
    println(remove(v, 1), v);   // the element at index 1
    v[0] = "first";
    println(v, slice(v, 1, 3));
    sort(v, fn(a, b) { return str(a) < str(b); });
    println(v);

    local grid = [[1, 2], [3, 4]];
    println(grid[1][0], len(grid));
}
```

```
$ kukel run vectors.kkl
[3, 1, 2, 10, 20] 5 3 20
20 [3, 1, 2, 10]
[99, 3, 1, 2, 10]
3 [99, 1, 2, 10]
["first", 1, 2, 10] [1, 2]
[1, 10, 2, "first"]
3 2
```

- `push(v, x, ...)` appends, `pop(v)` takes the last one off; `insert`
  and `remove` work at an index.
- `v[i] = x` replaces an element. An index outside `0 .. len(v) - 1` is an
  error (a `RangeError`): a vector does not grow by assigning past its
  end, use `push`.
- `sort(v)` sorts in place, with `<`; give it a function that says
  whether `a` goes before `b` to sort another way (here: as text). That
  `fn(a, b) { ... }` is a function written in place; chapter 8 is about
  them.
- `slice(v, start, end)` gives a new vector, a copy of that part.

## Maps

A map finds a value by its key. Keys are strings or numbers; the map
keeps them in the order they were added.

```
main() {
    local ages = {"Ann": 41, "Bo": 25};
    ages["Cy"] = 33;                // add
    ages["Ann"] = 42;               // replace
    println(
        ages,
        len(ages),
        ages["Bo"],
        ages["nobody"],
    );
    println(haskey(ages, "Cy"), delete(ages, "Bo"), ages);
    println(keys(ages), values(ages));

    local point = {"x": 1, "y": 2};
    local x = "y";
    println({x: 5}, point[x]);
}
```

```
$ kukel run maps.kkl
{"Ann": 42, "Bo": 25, "Cy": 33} 3 25 nil
true true {"Ann": 42, "Cy": 33}
["Ann", "Cy"] [42, 33]
{"y": 5} 2
```

- `m[k]` is `nil` for a key that is not there. `haskey(m, k)` tells a
  missing key from one that holds `nil`.
- `delete(m, k)` removes a key, and says whether it was there.
- `keys(m)` and `values(m)` give vectors, in the order of the map.
- In `{key: value}` the key is an expression: `{x: 5}` uses the
  *variable* `x`. Write `{"x": 5}` for the string.
- `1` and `1.0` are the same key.

## Walking through them

`for (local x in ...)` takes the elements of a vector, the keys of a map,
or the bytes of a string, one at a time. With two names it also gives the
index (or, for a map, the key and the value):

```
main() {
    local words = split("the cat saw the dog", " ");
    local counts = {};

    for (local w in words) {
        if (haskey(counts, w)) {
            counts[w]++;
        } else {
            counts[w] = 1;
        }
    }

    for (local word, n in counts) {
        println(word, n);
    }

    local numbered = [];

    for (local i, w in words) {
        push(numbered, str(i) + ":" + w);
    }
    println(join(numbered, " "));
}
```

```
$ kukel run words.kkl
the 2
cat 1
saw 1
dog 1
0:the 1:cat 2:saw 3:the 4:dog
```

`counts[w]++` adds one to the value stored under `w`. Chapter 6 has the
other loops.

## A spread of values

`v..` spreads a vector out: in a vector literal it inserts the elements,
in a call it passes them as separate arguments.

```
main() {
    local a = [1, 2], b = [3];
    println([0, a.., b.., 4]);
    println(max(a.., b..), min([5, 3, 9]..));
}
```

```
$ kukel run spread.kkl
[0, 1, 2, 3, 4]
3 3
```

## More functions

These are the built-in functions for strings and collections; the
[reference](../reference.md#10-built-in-functions) has the details of
each.

| Strings | Vectors | Maps |
|---|---|---|
| `len`, `find`, `split`, `join`, `slice`, `upper`, `lower`, `trim`, `replace`, `char`, `ord` | `len`, `push`, `pop`, `insert`, `remove`, `slice`, `sort`, `join`, `min`, `max` | `len`, `keys`, `values`, `haskey`, `delete` |

[The booklet](README.md) · previous: [2. Values and types](02-values.md) · next: [4. Variables and scope](04-scope.md)
