# Example programs

Small programs that use Kukel for something real. Run them from this
directory:

```
cd examples
kukel run grep.kkl -- -n -i error testdata/log.txt
```

| Program | What it does |
|---|---|
| [chat.kkl](chat.kkl) | a small chat server and its clients (module `zmq`, with `events`, `json` and `worker`): the server answers JSON requests (join, say, leave, status, stop) on a ROUTER socket and broadcasts on a PUB socket, with a heartbeat (`events::every`); a client asks with a REQ socket (`receive` with a timeout; `status` waits with `zmq::poll`), hears the broadcasts with an `onmessage` handler, and takes the server for gone when the heartbeat stops (`events::after`); `demo` runs the server in a worker and two clients in one process |
| [grep.kkl](grep.kkl) | prints the lines of files (or of standard input, also as `-`) that match a regular expression (module `re`), with `-i`, `-n`, `-v`, `-c`, `-l`, `-o`, `-F`, `-h`, and `-r` for directories (module `fs`); files and standard input are read a line at a time (`fs::open`, `fs::stdin()`), so large ones are not read into memory |
| [logstat.kkl](logstat.kkl) | statistics of a log file (or of standard input): entries per level and per hour, the time span and the errors; a regular expression with named groups takes the lines apart (module `re`), times are read and compared with module `time`, `-since TIME` and `-last DURATION` (before the newest entry) select entries, the report is built with a `strings::Builder`, `-json` prints it as JSON |
| [ninep.kkl](ninep.kkl) | a 9P file server of a small tree the program makes up (a file that counts its readers, one that keeps notes written to it, a control file that stops the server) and, if given, a directory of the disk (read-only, as `/files`), and a client that lists, reads and writes files of 9P servers (module `p9`); `demo` does both in one process |
| [notes.kkl](notes.kkl) | a small text editor in a window (module `tk`, so a kukel built with the tag `tk`): a File menu with New, Open, Save, Save as and Quit (also as Ctrl+N, Ctrl+O, Ctrl+S, Ctrl+Q), file dialogs, a question before changes are dropped, and a status line |
| [vi.kkl](vi.kkl) | a small vi in the terminal (module `term`, with `utf8` and `fs`): normal, insert and `:` command modes; moves `h j k l`, the arrows, `w b 0 ^ $ gg G` with counts (`3j`, `5G`); `i a I A o O`, `x`, `dd`, `yy`/`Y`, `p P`, `J`, `r`, one level of `u` (u again takes it back); `/` search with `n` and `N`; `:w [file]`, `:q`, `:q!`, `:wq`, `:x`, `:N` (a line), `:p` and `:%p`; Ctrl-G; a status line with the file, `[+]` when changed, and the position. Lines are vectors of characters (`utf8::chars`), so `é` is one; tabs show to the next multiple of 8 columns, long lines scroll sideways. When the output is not a terminal it draws nothing and prints its messages: `vi.tests` gives it keys from files (`testdata/vi-*.keys`) |
| [todo.kkl](todo.kkl) | a task list kept in a database (module `db` with objects in tables, `json`, `time`, `uuid`): `add` (with a due date and tags), `done`, `rm`, `tag`, `list` by due date (`-tag` for the tasks with a tag), `export` as JSON; tasks have UUID keys (made by `save`) and a number for users; tags are a `db::many` relation, loaded with `include` (one query for all tasks); each command a transaction; the commands come from the arguments or one per line from standard input, the database from `-db` (default `sqlite:todo.db`) |
| [wc.kkl](wc.kkl) | counts the lines, words and bytes of files in parallel: `-j N` workers (module `worker`) each get the next file as they finish one (module `events`); a directory counts the files below it (module `fs`); `-json` prints the counts as JSON; the output is the same for any number of workers |

## Kukel in a Go program

[embed/](embed) is a Go program that runs Kukel code (see
[docs/embedding.md](../docs/embedding.md)): it prices orders with discount
rules that users write as a Kukel script. The script gets a module of Go
functions (`shop`), and runs under the limits for code that is not trusted:
no files, no exit, only the modules `json` and `strings`, a step limit and a
time limit for each order. An error in the rules fails only that order.

```
go run ./examples/embed examples/embed/rules.kkl examples/embed/orders.json
```

`go test ./examples/embed` tests it, also with rules that go wrong.

## Tests

`notes.kkl` shows a window instead of printing. The tests of the module
`tk` (`make test-tk`, with a display or Xvfb) run it, as they run the `tk`
examples of the reference: each must open its window and end without an
error when the window is closed. They run here, in examples: the pictures
of the reference's example of images are `testdata/sun.png` and
`testdata/moon.png`. Two more tests use `xdotool`: one presses Ctrl+O
and Ctrl+Q in the text of `notes.kkl`, one clicks the reference's
settings example.

Each other program has a file `name.tests` that `go test ./cmd/kukel` (and so
`make test` and `make test-all`) runs, in this directory:

```
# a comment
$ -n error testdata/log.txt
6:2026-10-16 09:05:00 error retrying write (attempt 2)

$ -x a
! grep: unknown option -x
! usage: grep [-icFhlnorv] pattern [file ...]
exit 2
```

A case starts with `$ ` and the program's arguments (`$` alone: none):
`'...'` quotes an argument with spaces or backslashes (`''` is an empty
one; a `'` inside an argument cannot be written), and `< file` gives the
file as standard input. The lines after it are the expected standard
output, lines starting with `! ` the expected standard error (`!` alone:
an empty line), and a last line `exit N` the expected exit code (0 when
there is none). Blank lines and comments (`#`) at the end of a case,
before the next `$`, are skipped; inside the output they are part of it.
So the output cannot end with a blank line or a line starting with `#`,
no output line can start with `$ ` or `! `, and the last one cannot be
`exit N`. The files the programs read are in `testdata/`. The programs
are also kept formatted (`kukel fmt -l` lists none).
