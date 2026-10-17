# Example programs

Small programs that use Kukel for something real. Run them from this
directory:

```
cd examples
kukel run grep.kkl -- -n -i error testdata/log.txt
```

| Program | What it does |
|---|---|
| [grep.kkl](grep.kkl) | prints the lines of files (or of standard input) that match a regular expression (module `re`), with `-i`, `-n`, `-v`, `-c`, `-l`, `-o`, `-F`, `-h`, and `-r` for directories (module `fs`) |
| [ninep.kkl](ninep.kkl) | a 9P file server of a small tree the program makes up (a file that counts its readers, one that keeps notes written to it, a control file that stops the server) and, if given, a directory of the disk (read-only, as `/files`), and a client that lists, reads and writes files of 9P servers (module `p9`); `demo` does both in one process |
| [notes.kkl](notes.kkl) | a small text editor in a window (module `tk`, so a kukel built with the tag `tk`): a File menu with New, Open, Save, Save as and Quit (also as Ctrl+N, Ctrl+O, Ctrl+S, Ctrl+Q), file dialogs, a question before changes are dropped, and a status line |

## Tests

`notes.kkl` shows a window instead of printing. The tests of the module
`tk` (`make test-tk`, with a display or Xvfb) run it, as they run the `tk`
examples of the reference: each must open its window and end without an
error when the window is closed.

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

A case starts with `$ ` and the program's arguments: `'...'` quotes an
argument with spaces or backslashes, and `< file` gives the file as
standard input. The lines after it are the expected standard output, lines
starting with `! ` the expected standard error, and `exit N` the expected
exit code (0 when there is none). The files the programs read are in
`testdata/`. The programs are also kept formatted (`kukel fmt -l` lists
none).
