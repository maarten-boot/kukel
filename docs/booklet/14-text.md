# 14. Text and data

[The booklet](README.md) · previous: [13. Files and directories](13-files.md) · next: [15. Time and identifiers](15-time.md)

Much of programming is turning text into data and back. Four modules help:
`utf8` counts and changes text by character, `re` finds patterns, `json`
reads and writes the format most web services speak, and `strings`
builds long texts from many parts.

## Characters: `utf8`

Kukel strings are bytes, and the built-ins (`len`, `slice`, `upper`)
count and change bytes. For English text the two are the same; for "é",
"ñ" or "€", one character takes two or three bytes. The module `utf8`
works by character instead:

```
use utf8;

main() {
    local s = "Grüße aus Köln";
    println(len(s), utf8::len(s));
    println(upper(s), "/", utf8::upper(s));
    println(slice(s, 0, 4), "/", utf8::slice(s, 0, 4));
    println(utf8::chars("añ€"), utf8::codes("añ€"), utf8::char(0x263A));
    println(utf8::valid(s), utf8::valid("\xff"), utf8::fix("bad \xff byte"));
}
```

```
$ kukel run chars.kkl
17 14
GRüßE AUS KöLN / GRÜßE AUS KÖLN
Grü / Grüß
["a", "ñ", "€"] [97, 241, 8364] ☺
true false bad � byte
```

- `utf8::len` counts characters; `utf8::slice` cuts at characters.
  `slice(s, 0, 4)` gives four bytes, "Grü"; `utf8::slice(s, 0, 4)` four
  characters, "Grüß". (`slice(s, 0, 3)` would cut the `ü` in half.)
- `utf8::upper`, `utf8::lower` and `utf8::trim` know all of Unicode's
  letters and spaces.
- `utf8::chars` splits a string into characters, `utf8::codes` into their
  numbers; `utf8::char(code)` and `"\u{263A}"` go the other way.
- Bytes that are not UTF-8 (from a binary file, say) are kept as they are;
  `utf8::valid` finds them and `utf8::fix` replaces them.

## Patterns: `re`

A *regular expression* describes a pattern of text: `\d+` is "one or more
digits", `[A-Z]\w*` "a capital letter and then letters". The module `re`
finds, takes apart and replaces such patterns. In a Kukel string, a
backslash is written twice: `"\\d+"` is the pattern `\d+`.

```
use re;

main() {
    local date = re::compile("(\\d{4})-(\\d{2})-(\\d{2})");
    local text = "Due 2026-11-30, paid 2026-12-02.";
    println(date->match(text), date->find(text));
    println(date->findall(text));
    println(date->groups(text));
    println(date->replace(text, "$3/$2/$1"));
    println(re::compile("[,;]\\s*")->split("a, b;c ,d"));

    local word = re::compile("\\w+");
    println(word->replace("the quick brown fox", fn(m) { return upper(slice(m, 0, 1)) + slice(m, 1); }));
}
```

```
$ kukel run patterns.kkl
true 2026-11-30
["2026-11-30", "2026-12-02"]
["2026-11-30", "2026", "11", "30"]
Due 30/11/2026, paid 02/12/2026.
["a", "b", "c ", "d"]
The Quick Brown Fox
```

- `re::compile(pattern)` turns a pattern into a `re::Regexp` once, to be
  used many times. A second argument gives flags: `"i"` ignores case,
  `"m"` lets `^` and `$` match at every line, `"s"` lets `.` match a new
  line, `"U"` makes repetition lazy.
- `match` says whether it occurs; `find` gives the first match,
  `findall` all of them; `groups` gives the first match with the parts in
  parentheses.
- `replace` replaces every match, by a text in which `$1`, `$2` are the
  groups, or by what a function makes of the match.
- `split` cuts the text at every match.

Kukel uses Go's regular expressions: a match takes time in proportion to
the text, whatever the pattern, so a pattern from a user cannot hang the
program. The price: there are no back references (`\1`). The
[syntax](https://pkg.go.dev/regexp/syntax) is the familiar one of Perl,
Python and JavaScript.

## Data: `json`

JSON is the text format of most web services and many configuration
files. `json::decode` turns JSON text into Kukel values, and
`json::encode` turns values into JSON:

```
use json;

main() {
    local text = "{\"name\": \"Ann\", \"age\": 41, \"langs\": [\"Go\", \"Kukel\"], \"admin\": false, \"boss\": null}";
    local person = json::decode(text);
    println(
        person["name"],
        person["age"] + 1,
        person["langs"][1],
        person["admin"],
        person["boss"],
    );

    person["age"] = 42;
    push(person["langs"], "C");
    println(json::encode(person));
    println(json::encode({"ok": true, "items": [1, 2.5]}, "  "));

    try {
        json::decode("{\"name\": }");
    } catch (e) {
        println(e->kind, e->message);
    }
}
```

```
$ kukel run data.kkl
Ann 42 Kukel false nil
{"name":"Ann","age":42,"langs":["Go","Kukel","C"],"admin":false,"boss":null}
{
  "ok": true,
  "items": [
    1,
    2.5
  ]
}
ValueError json::decode: invalid JSON at byte 10: invalid character '}' where a value should be
```

- A JSON object becomes a map (in the order of the text), an array a
  vector, `null` is `nil`; numbers become ints or floats.
- `json::encode(v)` writes the opposite; with a second argument (spaces)
  it spreads the text over lines, for people to read.
- Invalid JSON is a `ValueError` that says where it goes wrong.
- Objects of classes are not JSON: turn them into maps first.

With `readfile` and `writefile` (chapter 13) that makes a configuration
file of a few lines of code:
`local config = json::decode(readfile("config.json"));`.

## Building text: `strings`

A `strings::Builder` collects text from many parts:

```
use strings;

table(rows) {
    local b = new strings::Builder();

    for (local r in rows) {
        b->add("| ", join(r, " | "), " |\n");
    }
    return b->str();
}

main() {
    print(table([["name", "age"], ["Ann", 41], ["Bo", 25]]));
}
```

```
$ kukel run table.kkl
| name | age |
| Ann | 41 |
| Bo | 25 |
```

`b->add(...)` appends values as `print` writes them, and gives `b`
again, so calls can be chained; `b->str()` is the text so far.
(`s = s + x` in a loop is fast too, so the builder is mostly a matter of
taste.)

[The booklet](README.md) · previous: [13. Files and directories](13-files.md) · next: [15. Time and identifiers](15-time.md)
