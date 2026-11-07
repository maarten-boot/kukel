# 5. Expressions and operators

[The booklet](README.md) · previous: [4. Variables and scope](04-scope.md) · next: [6. Statements](06-statements.md)

An expression computes a value: `2 + 3`, `len(v) > 0`, `x = y`. Kukel's
operators are those of C, with a few differences noted below.

## Arithmetic

```
main() {
    println(
        7 + 2,
        7 - 2,
        7 * 2,
        7 / 2,
        7 % 2,
        -7 / 2,
        -7 % 2,
    );
    println(
        7.0 / 2,
        7.5 % 2,
        2 + 3 * 4,
        (2 + 3) * 4,
    );
    println(
        pow(
            2,
            10,
        ),
        sqrt(16),
        abs(-3),
        round(2.5),
        floor(2.7),
        ceil(2.1),
    );
    println("ab" + "cd");
}
```

```
$ kukel run arithmetic.kkl
9 5 14 3 1 -3 -1
3.5 1.5 14 20
1024 4.0 3 3 2 3
abcd
```

- `+ - * / %` work on ints and floats; one float makes the result a float.
- `/` of two ints drops the fraction, toward zero; `%` has the sign of the
  left side.
- `+` also joins two strings (but not a string and a number: chapter 2).
- The math functions (`pow`, `sqrt`, `abs`, `round`, `floor`, `ceil`,
  `min`, `max`, `sin`, `cos`, `exp`, `log`, ...) are built-in functions,
  not operators.

## Comparing

`==` `!=` `<` `<=` `>` `>=` give `true` or `false`. `<` and the others
compare numbers with numbers and strings with strings (by their bytes);
`==` takes any two values (chapter 2).

```
main() {
    println(
        1 < 2,
        2 <= 2,
        1 == 1.0,
        "apple" < "banana",
        "Z" < "a",
    );
    println(
        nil == false,
        [1] == [1],
        "1" == 1,
    );
}
```

```
$ kukel run compare.kkl
true true true true true
false false false
```

## And, or, not

`&&` (and), `||` (or) and `!` (not) work on truth (chapter 2) and give
`true` or `false`. `&&` and `||` stop as soon as the answer is known: in
`a && b`, `b` is not computed when `a` is false. That makes checks such as
`i < len(v) && v[i] > 0` safe.

```
main() {
    local v = [3, 0];

    for (local i = 0; i < 3; i++) {
        println(
            i,
            i < len(v) && v[i] > 0,
            !(i < len(v)),
        );
    }
    println(
        nil || "default",
        0 || 1,
    );
}
```

```
$ kukel run logic.kkl
0 true false
1 false false
2 false true
true true
```

Unlike in JavaScript or Python, `nil || "default"` is `true`, not
`"default"`: `||` gives a `bool`. Choose with `?:` instead:
`x != nil ? x : "default"`.

## Choosing

`c ? a : b` is `a` when `c` is true, else `b`; only the chosen side is
computed:

```
main() {
    for (local n in [1, 2, 3]) {
        println(
            n,
            n % 2 == 0 ? "even" : "odd",
        );
    }
}
```

```
$ kukel run choose.kkl
1 odd
2 even
3 odd
```

## Assigning

`=` assigns, and is an expression: its value is the value assigned. The
operators `+= -= *= /= %=` (and `&= |= ^= <<= >>=`) change a variable with
its own value; `++` and `--` add or take one, before (`++x`: the new
value) or after (`x++`: the old value):

```
main() {
    local x = 10, y;
    x += 5;
    x *= 2;
    println(x);
    println(
        x++,
        x,
        ++x,
        x,
    );
    local a = y = 4;
    println(
        a,
        y,
    );

    local m = {"n": 1};
    m["n"] += 10;
    println(m);
}
```

```
$ kukel run assign.kkl
30
30 31 32 32
4 4
{"n": 11}
```

Anything that can hold a value can be assigned: a variable, `v[i]`,
`m[k]`, an object's member `p->x` (chapter 9).

## Bits

For ints there are the bitwise operators of C: `&` (and), `|` (or), `^`
(exclusive or), `~` (not), and the shifts `<<` and `>>`.

```
main() {
    println(
        5 & 3,
        5 | 3,
        5 ^ 3,
        ~5,
        1 << 4,
        -16 >> 2,
        0xFF & 0x0F,
    );
}
```

```
$ kukel run bits.kkl
1 7 6 -6 16 -4 15
```

## Precedence

From the operators that bind loosest to those that bind tightest:

| Operators | What |
|---|---|
| `=` `+=` `-=` ... | assigning, right to left: `a = b = 1` |
| `c ? a : b` | choosing, right to left |
| `\|\|` | or |
| `&&` | and |
| `\|` | bitwise or |
| `^` | bitwise exclusive or |
| `&` | bitwise and |
| `==` `!=` | equal |
| `<` `<=` `>` `>=` | order |
| `<<` `>>` | shifts |
| `+` `-` | adding |
| `*` `/` `%` | multiplying |
| `-x` `!x` `~x` `++x` `--x` | one operand, before it |
| `f(x)` `v[i]` `p->x` `x++` `x--` | calls, indexes, members; after it |

So `a + b * c` is `a + (b * c)`, and `x == 1 || x == 2` needs no
parentheses. When in doubt, add them: they cost nothing.

A comma between two expressions (`i++, j--`) is allowed only in the first
and last part of a `for` loop (chapter 6).

[The booklet](README.md) · previous: [4. Variables and scope](04-scope.md) · next: [6. Statements](06-statements.md)
