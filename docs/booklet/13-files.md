# 13. Files and directories

[The booklet](README.md) · previous: [12. Your own modules](12-modules.md) · next: [14. Text and data](14-text.md)

Part II is about the modules that come with kukel. This chapter is about
storage on disk: reading and writing files, whole or a line at a time,
and working with directories and paths. Three built-in functions handle
whole files; the module `fs` does everything else.

## Whole files

`readfile(path)` gives a file's contents as one string;
`writefile(path, s)` replaces a file with `s` (making it if needed);
`appendfile(path, s)` adds `s` at its end:

```
main() {
    writefile("todo.txt", "buy milk\n");
    appendfile("todo.txt", "call Ann\n");

    local text = readfile("todo.txt");
    print(text);
    println(len(split(trim(text), "\n")), "things to do");
}
```

```
$ kukel run whole.kkl
buy milk
call Ann
2 things to do
```

That is the simplest way, and right for files that fit in memory with
room to spare. A path is relative to the current directory unless it
starts at the root (`/home/ann/todo.txt`, or `C:\...` on Windows).

## Open files

For a large file, a log that grows, or a file written bit by bit,
`fs::open` gives an `fs::File`, which reads and writes as the program
goes. Always open it with `with`, so that it is closed however the block
ends:

```
use fs;

main() {
    with (local out = fs::open("scores.txt", "w")) {
        out->println("Ann", 31);
        out->println("Bo", 27);
        out->write("Cy 40\n");
    }

    with (local more = fs::open("scores.txt", "a")) {
        more->println("Di", 35);
    }

    local best = "", top = -1;

    with (local f = fs::open("scores.txt")) {
        for (local i, line in f) {
            local parts = split(line, " ");
            local score = int(parts[1]);
            println(i + 1, parts[0], score);

            if (score > top) {
                best = parts[0];
                top = score;
            }
        }
    }
    println("best:", best, top);
}
```

```
$ kukel run scores.kkl
1 Ann 31
2 Bo 27
3 Cy 40
4 Di 35
best: Cy 40
```

The second argument of `fs::open` is the *mode*, as in C: `"w"` writes
and empties the file first, `"a"` appends, and without one the file is
read. `out->println(...)` writes as the built-in does, `out->write(...)`
strings exactly as they are. `for (local i, line in f)` walks the lines,
without their line ends, and counts them from 0.

Writing is buffered, so writing many small pieces is fast; `close`, which
`with` calls, writes out what is left, and `f->flush()` does it earlier,
for a log someone is watching. The other modes and all that a file can do
are in the [reference](../reference.md#fs).

## Pieces and positions

Strings are bytes, so a file can hold anything, not only text.
`f->read(n)` reads up to `n` bytes; `f->seek(offset)` and `f->tell()`
move and tell the position. A file in memory, `fs::memory(text)`, works
like a file on disk, which is handy for tests:

```
use fs;

main() {
    local f = fs::memory("0123456789");
    println(f->read(3), f->tell(), f->read(2));
    f->seek(-2, "end");
    println(f->read());
    f->seek(0);
    f->write("AB");
    println(f->text());
}
```

```
$ kukel run pieces.kkl
012 3 34
89
AB23456789
```

`f->read()` without a number reads the rest; it gives `nil` when nothing
is left.

## Standard input and output

`fs::stdin()`, `fs::stdout()` and `fs::stderr()` are the program's
standard streams as `fs::File`s. So a function can take any file, and be
given a file on disk, a stream or a file in memory:

```
use fs;

report(out, names) {
    for (local i, n in names) {
        out->println(str(i + 1) + ".", n);
    }
}

main() {
    report(fs::stdout(), ["Ann", "Bo"]);

    local mem = fs::memory();
    report(mem, ["Cy"]);
    print(mem->text());
}
```

```
$ kukel run report.kkl
1. Ann
2. Bo
1. Cy
```

A program that reads its input line by line, as `grep` or `sort` do,
uses `readline()`, or `for (local line in fs::stdin())`.

## Directories

```
use fs;

main() {
    local dir = fs::tempdir("demo-*");
    fs::mkdirall(fs::join(dir, "src", "lib"));
    writefile(fs::join(dir, "src", "main.kkl"), "main() {}\n");
    writefile(
        fs::join(
            dir,
            "src",
            "lib",
            "util.kkl",
        ),
        "f() {}\n",
    );
    writefile(fs::join(dir, "notes.txt"), "remember\n");

    for (local e in fs::readdir(dir)) {
        println(e["name"], e["dir"] ? "(directory)" : e["size"]);
    }

    local sources = 0;
    fs::walk(dir, fn(path, e) {
        if (fs::ext(path) == ".kkl") { sources++; }
        return true;                       // false: do not go into this directory
    });
    println(sources, "Kukel files");

    local p = fs::join(
        dir,
        "src",
        "lib",
        "util.kkl",
    );
    println(
        fs::base(p),
        fs::ext(p),
        fs::base(fs::dir(p)),
        fs::exists(p),
    );
    fs::rename(fs::join(dir, "notes.txt"), fs::join(dir, "old-notes.txt"));
    println(fs::exists(fs::join(dir, "notes.txt")), fs::stat(fs::join(dir, "old-notes.txt"))["size"]);
    fs::removeall(dir);
    println(fs::exists(dir));
}
```

```
$ kukel run dirs.kkl
notes.txt 9
src (directory)
2 Kukel files
util.kkl .kkl lib true
false 9
false
```

An *entry*, from `fs::readdir` (each one in a directory, by name) or
`fs::stat` (one path), is a map: its `name`, its `size`, whether it is a
directory (`dir`), and more. `fs::walk` visits a directory and everything
below it; when its function returns `false` for a directory, `walk` stays
out of it (say, `.git`). Build paths with `fs::join`, which uses the
system's separator (`/`, or `\` on Windows), and take them apart with
`fs::base`, `fs::dir` and `fs::ext`: then the program works on every
system. `fs::tempdir` makes a directory with a name no one else has,
`fs::mkdirall` a directory and the ones above it, `fs::rename` renames or
moves, and `fs::removeall` removes a path and everything below it.

The [reference](../reference.md#fs) lists all of `fs`: the other fields of
an entry, making and removing directories, permissions, symbolic links,
and what each function does when something is in the way.

## Replacing a file safely

A program that is killed halfway through writing a file leaves half a
file. Write the new version to a temporary file next to the old one
(`fs::temp` makes one, with a name no one else has, in the directory
given), close it, and rename it over the old one: someone reading the file sees
the old version or the new one, never half of it.

```
use fs;

save(path, text) {
    local tmp = fs::temp(fs::base(path) + "-*", fs::dir(path));
    tmp->write(text);
    tmp->close();
    fs::rename(tmp->name(), path);
}

main() {
    save("settings.txt", "colour = blue\n");
    save("settings.txt", "colour = green\n");
    print(readfile("settings.txt"));
}
```

```
$ kukel run save.kkl
colour = green
```

`f->lock()` takes a lock on a file, so that two copies of a program do
not write it at the same time (the [reference](../reference.md#fs) has
the details).

## When it goes wrong

A file that is not there, a directory that cannot be removed, a disk
that is full: these are an `IOError`, whose message names the function
and the path:

```
use fs;

main() {
    try {
        readfile("no-such-file.txt");
    } catch (e) {
        println(e->kind, e->message);
    }

    try {
        fs::readdir("no-such-dir");
    } catch (e) {
        println(e->kind, e->message);
    }
}
```

```
$ kukel run errors.kkl
IOError readfile: no-such-file.txt: no such file or directory
IOError fs::readdir: no-such-dir: no such file or directory
```

[The booklet](README.md) · previous: [12. Your own modules](12-modules.md) · next: [14. Text and data](14-text.md)
