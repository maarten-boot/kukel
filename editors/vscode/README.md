# Kukel for Visual Studio Code

Syntax highlighting for Kukel (`.kkl` files), and debugging with
`kukel dap`: breakpoints (also with a condition, a hit count or a log
message), stepping, the call stack, variables (also changed while the
program is stopped), and evaluating expressions.

The extension runs the `kukel` command, which must be installed: in your
`PATH`, or named in the setting `kukel.path`. See
[docs/debugging.md](https://github.com/maarten-boot/kukel/blob/main/docs/debugging.md)
for the launch configuration and other editors.

Build it in the kukel repository with `make vscode`, and install the
`.vsix` file it makes: `code --install-extension dist/kukel-vscode-*.vsix`.
