// The Kukel extension: debugging runs "kukel dap" (the Debug Adapter
// Protocol on standard input and output; see docs/debugging.md in the
// kukel repository). Syntax highlighting needs no code.
const vscode = require("vscode");

function activate(context) {
  context.subscriptions.push(
    vscode.debug.registerDebugAdapterDescriptorFactory("kukel", {
      createDebugAdapterDescriptor() {
        const kukel = vscode.workspace.getConfiguration("kukel").get("path") || "kukel";
        return new vscode.DebugAdapterExecutable(kukel, ["dap"]);
      },
    }),
    // F5 without a launch.json debugs the Kukel file in the editor.
    vscode.debug.registerDebugConfigurationProvider("kukel", {
      resolveDebugConfiguration(folder, config) {
        if (!config.type && !config.request && !config.name) {
          const editor = vscode.window.activeTextEditor;
          if (editor && editor.document.languageId === "kukel") {
            config.type = "kukel";
            config.request = "launch";
            config.name = "Debug the Kukel program";
            config.program = "${file}";
            config.cwd = "${fileDirname}";
          }
        }
        if (!config.program) {
          return vscode.window.showInformationMessage("Name the .kkl file to debug as \"program\".").then(() => undefined);
        }
        // Without cwd, the program would run (and its program, files and
        // stdin be found) in VS Code's own working directory.
        if (!config.cwd) {
          config.cwd = folder ? folder.uri.fsPath : "${fileDirname}";
        }
        return config;
      },
    })
  );
}

function deactivate() {}

module.exports = { activate, deactivate };
