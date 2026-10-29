# Kukel for Geany

`filetypes.Kukel.conf` makes Geany (2.x) know Kukel (`.kkl` files):

- highlighting: keywords, built-in functions, strings and characters with
  their escapes, numbers, and the three kinds of comment (`//`, `/* */`,
  and `#` at the start of a line, so also `#!/usr/bin/env kukel`);
- the symbol list: classes with their fields and member functions, and
  the functions;
- comments with Ctrl-E, folding of blocks, an indentation of 4 spaces (as
  `kukel fmt` writes);
- the Build menu: Check (`kukel check`), Format (`kukel fmt -w`, then
  reload the file) and Execute (`kukel run`, F5); a click on an error, also
  on a line of an exception's trace, goes to its line.

## Installing

Copy the file to Geany's configuration directory:

```sh
mkdir -p ~/.config/geany/filedefs
cp editors/geany/filetypes.Kukel.conf ~/.config/geany/filedefs/
```

and tell Geany which files are Kukel: in Geany, Tools → Configuration
Files → `filetype_extensions.conf`, add the `Kukel` lines to the two
sections (create them when they are not there), and save:

```ini
[Extensions]
Kukel=*.kkl;

[Groups]
Programming=Kukel;
```

(When `Programming=` is already there, add `Kukel;` to its list.) Then
restart Geany. Kukel is now in Document → Set Filetype → Programming
languages, and `.kkl` files open with it. The Build menu runs the `kukel`
in your `PATH`; for another one, change the commands in Build → Set Build
Commands.

On Windows the directory is `%APPDATA%\geany` instead of
`~/.config/geany`.

## What it does not know

Geany has no lexer of its own for Kukel; this uses its C lexer, with
Kukel's words. So:

- a `#` comment after code on the same line (`f(); # note`) is not shown
  as a comment (one at the start of a line is);
- a string that spans lines is shown as an unterminated string;
- the symbol list takes Geany's C# parser (of the parsers Geany has, the one
  that reads Kukel's classes and functions without missing any): `global`
  and `const` names are not in it.
