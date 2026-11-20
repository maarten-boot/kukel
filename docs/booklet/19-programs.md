# 19. Running programs

[The booklet](README.md) · previous: [18. Networking](18-networking.md) · next: [20. The terminal](20-terminal.md)

Much of the work on a computer is done by programs that are already
there: `git`, `go`, `grep`, `tar`, `ssh`. The module `proc` runs them from
Kukel: it starts a program, gives it input, takes its output, and says how
it ended. It is what bash scripts do, without bash's traps.

The one rule that makes the difference: a program is a **vector of
strings**, the program and its arguments, and each argument reaches the
program exactly as written. Nothing splits it at spaces, expands `*` or
`$HOME`, or reads `;` as the end of a command:

```
use proc;

main() {
    local r = proc::run(["printf", "%s|", "one two", "$HOME", "*"]);
    println(r["out"]);
    println(r["code"], r["signal"], r["err"], typeof(r["time"]));

    local words = proc::run(["wc", "-w"], {"input": "the quick brown fox\n"});
    println(trim(words["out"]), "words");
}
```

```
$ kukel run run.kkl
one two|$HOME|*|
0 nil nil float
4 words
```

- `proc::run(cmd)` waits for the program and gives a map: the exit
  `code`, the `signal` that ended it (`nil` here), what it wrote to its
  standard output (`out`, captured by default), its errors (`err`, which
  go to your terminal by default, so `nil`), and the seconds it ran.
- `"input"` is text for the program's standard input; without it the
  program reads nothing.
- In bash, `printf "%s|" $x` with `x="one two"` gives two arguments, and
  `$x` with a `*` in it lists your files. In Kukel a value is one argument,
  whatever it holds, so a file name or a user's answer can never turn into
  a command.

`proc` finds the program on `PATH`, as a shell does. These examples use
programs every Unix system has; Windows has others (`proc::which(name)`
tells whether one is there).

## When a program fails

A program that fails, with an exit code other than 0, is an exception, a
`proc::ProcError`, unless you ask to look yourself (`"check": false`):

```
use proc;

main() {
    try {
        proc::run(["cat", "no-such-file"], {"err": "capture"});
    } catch (e) {
        println(e->kind, e->code);
        println(e->command);
    }

    local r = proc::run(["cat", "no-such-file"], {"err": "null", "check": false});
    println("cat said", r["code"]);

    try {
        proc::run(["sleep", "10"], {"timeout": 0.2});
    } catch (e) {
        println(e->message);
    }
}
```

```
$ kukel run fail.kkl
ProcError 1
["cat", "no-such-file"]
cat said 1
proc::run: ["sleep", "10"] timed out after 0.2 s
```

- The `ProcError` carries the `command`, its `code`, the `signal` that
  ended it if any, and `err`: the end of what it wrote to standard error,
  when that was captured (`"err": "capture"`). Its message ends with that
  error's last line, so an uncaught failure explains itself.
- With `"check": false` the result is yours to look at; `"err": "null"`
  drops the errors.
- `"timeout"` ends a program that takes too long: TERM, then KILL two
  seconds later. Whatever the program started is ended with it.
- So a script does not go on after a failed step without saying so: the
  `set -e` of bash, but an exception you can catch.

The other options say where things go: `"out"` and `"err"` take
`"capture"`, `"inherit"` (your terminal), `"null"`, `{"file": path}` or
`{"append": path}`, and `"err": "out"` puts the errors into the output,
in order. `"cwd"` is the directory the program runs in, `"env"` a map of
environment variables to set (`nil` removes one).

## Pipelines

`proc::pipe` connects programs as a shell's `|` does, through the
operating system's pipes: the data goes from program to program without
passing through Kukel, and still no shell is involved. Here, the two words
used most:

```
use proc;

main() {
    local words = "the cat and the dog and the bird\n";
    local r = proc::pipe([["tr", " ", "\n"], ["sort"], ["uniq", "-c"], ["sort", "-rn"], ["head", "-n", "2"]], {"input": words});
    print(r["out"]);
    println(r["codes"]);
}
```

```
$ kukel run pipe.kkl
      3 the
      2 and
[0, 0, 0, 0, 0]
```

`codes` has the exit code of every program. With `check` (the default),
any program that fails fails the whole pipeline, as bash's `pipefail`
does; one that is ended because the next program stopped reading (as
`head` does) does not count.

## Talking to a program

`proc::start` starts a program and gives a `proc::Process` at once, while
the program runs. With `"pipe"` for `"stdin"` and `"out"`, its input and
output are files to write to and read from (the `fs::File`s of chapter
13):

```
use proc;

main() {
    local p = proc::start(["sh", "-c", "while read line; do echo \"heard: $line\"; done"], {"stdin": "pipe", "out": "pipe"});

    for (local word in ["hello", "there"]) {
        p->stdin()->write(word + "\n");
        println(p->stdout()->readline());
    }
    println(p->wait()["code"], p->running());
}
```

```
$ kukel run talk.kkl
heard: hello
heard: there
0 false
```

Each `write` reaches the program at once. `p->wait()` closes the input
(so a program that reads to the end of its input ends), waits, and gives
`run`'s map. `p->kill()` sends a signal, `p->running()` says whether it
still runs.

## A shell when you want one

For what a shell does best, globs, `&&`, a pipeline written on one line,
`proc::sh` runs a script with `/bin/sh -c`. Values never go into the
script's text: `"args"` hands them to it as `$1`, `$2`, ...:

```
use proc;

main() {
    local name = "notes; rm -rf ~";
    local r = proc::sh("echo \"saving $1\" && echo \"$# argument\"", {"args": [name]});
    print(r["out"]);
}
```

```
$ kukel run sh.kkl
saving notes; rm -rf ~
1 argument
```

Had the name been glued into the text (`"echo saving " + name`), the
shell would have run `rm -rf ~`. Windows has no `/bin/sh`; there
`proc::sh` is an error.

## Programs in the event loop

A program can also report to the event loop of chapter 17, so that a
program reacts to output as it comes while it does other things:

```
use proc;
use events;

main() {
    local p = proc::start(["sh", "-c", "echo first; sleep 0.1; echo second"], {"out": "pipe"});
    local lines = 0;

    p->onlines(fn(line) {
        if (line != nil) {
            lines++;
            println("line:", line);
        }
    });
    p->onexit(fn(r) { println("done:", lines, "lines, code", r["code"]); });
    events::run();
}
```

```
$ kukel run events.kkl
line: first
line: second
done: 2 lines, code 0
```

`p->onlines(f)` calls `f` for each line of the output (`"err"` as a
second argument for the errors), and `f(nil)` at the end; `p->onexit(f)`
calls `f` with the result when the program has ended, after its last
lines. `events::run()` returns when both are done.

## A bash script in Kukel

A script that reports how many errors each log file has, the worst first:

```sh
#!/bin/sh
for f in logs/*.log; do
    echo "$f: $(grep -c ERROR "$f")"
done | sort -t: -k2 -rn
```

It works until a file name has a space or a colon in it, or there is no
`.log` file at all (then `*.log` is taken as a name), and `grep` saying
"no match" (code 1) and "cannot read the file" (code 2) look the same.
In Kukel:

```
use fs;
use proc;

main() {
    fs::mkdirall("logs");
    writefile("logs/a.log", "ok\nERROR disk\nERROR net\n");
    writefile("logs/b.log", "ok\n");
    writefile("logs/c.log", "ERROR once\n");

    local counts = [];

    for (local f in fs::glob(fs::join("logs", "*.log"))) {
        local r = proc::run(["grep", "-c", "ERROR", f], {"check": false});

        if (r["code"] > 1) {
            throw new proc::ProcError("grep failed on " + f, ["grep", f], r["code"]);
        }
        push(counts, [int(trim(r["out"])), fs::base(f)]);
    }
    sort(counts, fn(a, b) { return a[0] > b[0]; });

    for (local c in counts) {
        println(c[1] + ":", c[0]);
    }
}
```

```
$ kukel run errors.kkl
a.log: 2
c.log: 1
b.log: 0
```

- `fs::glob` gives the matching names as a vector (none: `[]`), each one
  value however it is spelled.
- `grep -c` exits with 1 when it counts no lines: that is not a failure
  here, so `check` is off and the code is looked at: 2 and more is.
- The sorting is Kukel's own, on numbers, not on text.

## What it may not do

`proc` can do anything the user who runs `kukel` can, so a Go program
that embeds Kukel gives it to scripts only when it names its Go part,
`process`, among the modules they may use (appendix C). And when you stop
a `kukel run` with Ctrl-C, the programs it started stop with it.

The full list of options and results is in the
[reference](../reference.md#proc).

[The booklet](README.md) · previous: [18. Networking](18-networking.md) · next: [20. The terminal](20-terminal.md)
