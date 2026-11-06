# The Kukel booklet

<p align="center"><img src="../Kukel-text-small.png" alt="The Kukel logo: a shield of green and blue lines around a question mark, above the word Kukel" width="160"></p>

Kukel is a small programming language with C-like syntax: classes,
closures, generators and exceptions, compiled to bytecode for a small
virtual machine written in Go. This booklet teaches it from the first
program on. Part I is the language; Part II, the modules that come with
it; the appendices, how Go programs and Kukel work together.

Every program in the booklet is run by kukel's tests, and what it prints
is what you see here. Each release on GitHub also has the whole booklet as
one PDF, `kukel-booklet-<version>.pdf`. The rules in full detail are in the
[reference](../reference.md); this booklet explains them in order.

<p align="center"><img src="components.svg" alt="Kukel's components: kukel in the centre; above it presentation, text (print, term) and gui (tk); below it storage, files (fs) and databases (db); to its right communication, net, zmq and p9" width="600"></p>

The language sits in the middle. Around it, modules connect a program to
the world: to people through text and windows (presentation), to files
and databases (storage), and to other programs (communication). Part II
takes them one by one.

## Part I: the language

1. [Getting started](01-getting-started.md): installing, the first
   program, the commands
2. [Values and types](02-values.md): numbers, strings, `nil`, true and
   false; what is equal and what is shared
3. [Strings, vectors and maps](03-collections.md)
4. [Variables and scope](04-scope.md): `local`, `global`, `const`, and
   where a name can be seen
5. [Expressions and operators](05-expressions.md)
6. [Statements](06-statements.md): `if`, the loops, `switch`, labels,
   `with`
7. [Functions](07-functions.md): parameters, defaults, the rest of the
   arguments, recursion
8. [Closures](08-closures.md): functions as values, function literals,
   bound methods
9. [Classes and member functions](09-classes.md): objects, constructors,
   inheritance, static members
10. [Exceptions](10-exceptions.md): `throw`, `try`, `catch`, `finally`,
    and the kinds of errors
11. [Generators](11-generators.md): `yield`, `yield from`, `send`, and
    classes `for` can walk
12. [Your own modules](12-modules.md): splitting a program into files,
    and the module `iter`

## Part II: the modules

13. [Files and directories](13-files.md): whole files, open files,
    directories, paths (`fs`)
14. [Text and data](14-text.md): characters (`utf8`), patterns (`re`),
    JSON (`json`), building text (`strings`)
15. [Time and identifiers](15-time.md): dates, times and time zones
    (`time`), UUIDs (`uuid`)
16. [Databases](16-databases.md): SQLite, PostgreSQL and MySQL, and
    objects in tables (`db`)
17. [Events and workers](17-events.md): the event loop (`events`), work in
    parallel (`worker`)
18. [Networking](18-networking.md): TCP and UDP (`net`), messages (`zmq`),
    files over the network (`p9`)
19. [The terminal](19-terminal.md): a key at a time (`term`)
20. [The tools](20-tools.md): formatting, the debugger, the profiler,
    building
21. [Windows](21-windows.md): windows, forms, menus and drawing (`tk`, in
    `kukel-tk`)

## Appendices

*To come:* embedding Kukel in Go; Go in Kukel (host modules); code that
is not trusted; keywords, operators, the grammar and compatibility.
