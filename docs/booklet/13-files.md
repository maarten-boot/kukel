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

    with (local more = fs::open("scores.txt", "a")) more->println("Di", 35);

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

- The second argument of `fs::open` is the *mode*, as in C: `"r"` reads
  (the default), `"w"` writes and empties the file first, `"a"` appends,
  `"x"` makes a new file and fails if there is one. With a `+` the file
  is open both ways.
- `out->println(...)` and `out->print(...)` write as the built-ins do;
  `out->write(s, ...)` writes strings exactly as they are.
- `for (local line in f)` walks the lines, without their line ends. With
  two names it counts them, from 0. `f->readline()` reads one line, `nil`
  at the end.
- Writing is buffered, so writing many small pieces is fast; `close`
  (which `with` calls) writes out what is left. `f->flush()` does it
  earlier, for a log someone is watching.

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
    for (local i, n in names) out->println(str(i + 1) + ".", n);
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
    writefile(fs::join(dir, "src", "lib", "util.kkl"), "f() {}\n");
    writefile(fs::join(dir, "notes.txt"), "remember\n");

    for (local e in fs::readdir(dir)) println(e["name"], e["dir"] ? "(directory)" : e["size"]);

    local sources = 0;
    fs::walk(dir, fn(path, e) {
        if (fs::ext(path) == ".kkl") sources++;
        return true;                       // false: do not go into this directory
    });
    println(sources, "Kukel files");

    local p = fs::join(dir, "src", "lib", "util.kkl");
    println(fs::base(p), fs::ext(p), fs::base(fs::dir(p)), fs::exists(p));
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

- `fs::readdir(dir)` gives the *entries* of a directory, by name. An
  entry is a map: `name`, `size`, `dir` (a directory?), `link` (a
  symbolic link?), `mode` (the permissions, as `ls -l` shows them:
  `"-rw-r--r--"`) and `time` (when it last changed). `fs::stat(path)`
  gives the entry of one path.
- `fs::walk(dir, f)` calls `f(path, entry)` for everything below `dir`,
  depth first. When `f` returns `false` for a directory, `walk` skips
  what is in it (say, `.git`).
- `fs::mkdir` makes a directory, `fs::mkdirall` also the ones above it;
  `fs::remove` removes a file or an empty directory, `fs::removeall`
  everything below a path. `fs::rename` renames or moves.
- `fs::join` puts paths together with the right separator for the system
  (`/`, or `\` on Windows); `fs::base`, `fs::dir` and `fs::ext` take them
  apart.
- `fs::tempdir` and `fs::temp` make a new directory or file with a name no
  one else has.

## Replacing a file safely

A program that is killed halfway through writing a file leaves half a
file. Write the new version to a temporary file next to the old one,
close it, and rename it over the old one: someone reading the file sees
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
