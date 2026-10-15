# Example programs

Small programs that use Kukel for something real. Run them from this
directory:

```
cd examples
kukel run grep.kkl -- -n -i error testdata/log.txt
```

| Program | What it does |
|---|---|
| [grep.kkl](grep.kkl) | prints the lines of files (or of standard input) that match a regular expression (module `re`), with `-i`, `-n`, `-v`, `-c`, `-l`, `-o`, `-F` and `-h` |

## Tests

Each program has a file `name.tests` that `go test ./cmd/kukel` (and so
`make test` and `make test-all`) runs, in this directory:

```
# a comment
$ -n error testdata/log.txt
6:2026-10-16 09:05:00 error retrying write (attempt 2)

$ -x a
! grep: unknown option -x
exit 2
```

A case starts with `$ ` and the program's arguments: `'...'` quotes an
argument with spaces or backslashes, and `< file` gives the file as
standard input. The lines after it are the expected standard output, lines
starting with `! ` the expected standard error, and `exit N` the expected
exit code (0 when there is none). The files the programs read are in
`testdata/`. The programs are also kept formatted (`kukel fmt -l` lists
none).
