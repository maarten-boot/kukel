# 6. Statements

[The booklet](README.md) · previous: [5. Expressions and operators](05-expressions.md) · next: [7. Functions](07-functions.md)

Statements do things one after the other: decide, repeat, jump. Kukel has
those of C, plus a loop over collections and `with`, which cleans up
after itself.

## Blocks and `if`

A block, `{ ... }`, groups statements into one. `if` runs a statement
when a value is true (chapter 2), `else` when it is not; `else if`
chains several choices:

```
grade(score) {
    if (score >= 90) {
        return "A";
    }
    else if (score >= 75) {
        return "B";
    }
    else if (score >= 50) {
        return "C";
    }
    else {
        return "F";
    }
}

main() {
    for (local s in [95, 80, 51, 12]) {
        print(grade(s));
    }
    println();

    local temperature = 31;

    if (temperature > 30) {
        println("hot");
        println("drink water");
    }
}
```

```
$ kukel run grades.kkl
ABCF
hot
drink water
```

Without braces, `if` takes the one statement that follows. `kukel fmt
-strict` adds braces wherever they are missing, for those who prefer them
always.

## Loops

`while` repeats as long as a value is true; `do ... while` runs its body
first and tests after; `for` has room for a start, a test and a step:

```
main() {
    local n = 1;

    while (n < 100) {
        n *= 3;
    }
    println(n);

    do {
        n -= 50;
    } while (n > 0);
    println(n);

    for (local i = 0, j = 10; i < j; i += 3, j -= 3) {
        println(
            i,
            j,
        );
    }

    for (;;) {                      // no test: until break
        n++;

        if (n == 3) {
            break;
        }
    }
    println(n);
}
```

```
$ kukel run loops.kkl
243
-7
0 10
3 7
3
```

`break` leaves the innermost loop at once; `continue` skips to its next
round. In a `for` loop the comma joins expressions in the first and last
part: `i += 3, j -= 3`.

## Walking a collection

`for (local x in c)` takes the values of `c` one at a time: the elements
of a vector, the keys of a map, the bytes of a string, or the values of a
generator (chapter 11). With two names it gives the position too:

```
main() {
    local colours = ["red", "green", "blue"];
    local stock = {"pens": 12, "ink": 0};

    for (local c in colours) {
        println(c);
    }

    for (local i, c in colours) {
        println(
            i,
            c,
        );
    }

    for (local item, n in stock) {
        if (n == 0) {
            continue;
        }
        println(
            item,
            n,
        );
    }

    for (local b in "hi") {
        println(
            b,
            char(b),
        );
    }
}
```

```
$ kukel run foreach.kkl
red
green
blue
0 red
1 green
2 blue
pens 12
104 h
105 i
```

## `switch`

`switch` compares one value with several constant values, with `==`:

```
const QUIT = "q";

describe(c) {
    switch (c) {
        case "a":
        case "e":
        case "i":
        case "o":
        case "u":
            return "a vowel";
        case QUIT:
            return "the end";
        case " ":
            break;
        default:
            {
                local code = ord(c);
                return "a consonant, code " + str(code);
            }
    }
    return "a space";
}

main() {
    for (local c in ["e", "k", " ", "q"]) {
        println(
            c,
            "is",
            describe(c),
        );
    }
}
```

```
$ kukel run switch.kkl
e is a vowel
k is a consonant, code 107
  is a space
q is the end
```

- Control *falls through* from one `case` to the next until a `break` (or
  `return`), as in C: the five vowels share one answer.
- The values after `case` are constants: literals, or names declared
  with `const`.
- A `local` directly under a `case` is not allowed; put it in a block, as
  under `default` here.

## Labels

A loop inside a loop sometimes needs to leave both. Give the outer loop a
*label*, and `break` or `continue` it by name:

```
main() {
    local grid = [[1, 2, 3], [4, -5, 6], [7, 8, 9]];

    search: for (local r = 0; r < len(grid); r++) {
        for (local c = 0; c < len(grid[r]); c++) {
            if (grid[r][c] < 0) {
                println(
                    "negative at",
                    r,
                    c,
                );
                break search;
            }
        }
    }
}
```

```
$ kukel run labels.kkl
negative at 1 1
```

## `with`: clean up, whatever happens

Some things must be closed when you are done with them: a file, a
database connection, a network connection. `with` declares them and
closes them when its statement ends, also when it ends early by
`return`, `break` or an exception (chapter 10):

```
class Resource {
    name;
    Resource(name);
    work();
    close();
}

Resource::Resource(name) {
    this->name = name;
    println(
        "open",
        name,
    );
}

Resource::work() {
    println(
        "work with",
        name,
    );
}
Resource::close() {
    println(
        "close",
        name,
    );
}

main() {
    with (local a = new Resource("a"), b = new Resource("b")) {
        a->work();
        b->work();
    }
    println("done");
}
```

```
$ kukel run with.kkl
open a
open b
work with a
work with b
close b
close a
done
```

`with` calls `close()` on each of its variables, the last one first. It
works with anything that has a member function `close()`: the files,
connections and sockets of Part II, and objects of your own classes, as
here (classes are chapter 9).

## `return`, `throw`, `try`

`return` ends a function and gives its value (chapter 7). `throw`, `try`,
`catch` and `finally` are about exceptions (chapter 10); `yield` is for
generators (chapter 11).

## The keywords

These words are the language's own, and cannot be used as names:

```
break  case  catch  class  const  continue  default  do  else  false
finally  fn  for  global  if  in  local  new  nil  return  static
switch  this  throw  true  try  use  while  with  yield
```

`from` (in `yield from`) is a word only where it stands: a variable may
still be called `from`. Within version 1 no new keywords are added
([compatibility](../compatibility.md)), so a name that works today keeps
working.

[The booklet](README.md) · previous: [5. Expressions and operators](05-expressions.md) · next: [7. Functions](07-functions.md)
