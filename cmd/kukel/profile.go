package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/maarten-boot/kukel/internal/builtins"
	"github.com/maarten-boot/kukel/internal/profile"
	"github.com/maarten-boot/kukel/internal/vm"
)

const profileUsage = "kukel profile [-o report.txt] [-cpu] a.kkl ... [-- args]"

// kukel profile (TODO.md Q78): runs a program as kukel run does, profiled
// (the main interpreter and its workers), then writes the report: to
// standard error, or to the file of -o. -cpu leaves waiting out.
func cmdProfile(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	files, progArgs := args, []string(nil)
	for i, a := range args {
		if a == "--" {
			files, progArgs = args[:i], args[i+1:]
			break
		}
	}
	var reportFile string
	cpu := false
	for len(files) > 0 && strings.HasPrefix(files[0], "-") && files[0] != "-" {
		switch files[0] {
		case "-cpu":
			cpu = true
			files = files[1:]
		case "-o":
			if len(files) < 2 {
				fmt.Fprintf(stderr, "kukel profile: -o needs a file\nusage: %s\n", profileUsage)
				return 2
			}
			reportFile, files = files[1], files[2:]
		default:
			fmt.Fprintf(stderr, "kukel profile: unknown option %s\nusage: %s\n", files[0], profileUsage)
			return 2
		}
	}
	if len(files) == 0 {
		fmt.Fprintln(stderr, "usage:", profileUsage)
		return 2
	}
	if badFiles("profile", files, profileUsage, stderr) {
		return 2
	}
	prog, err := compileFiles(files)
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	report := stderr
	if reportFile != "" {
		f, err := os.Create(reportFile)
		if err != nil {
			fmt.Fprintln(stderr, "kukel:", err)
			return 1
		}
		defer f.Close()
		report = f
	}

	out := bufio.NewWriter(stdout)
	defer out.Flush()
	machine, err := vm.NewWith(prog, builtins.Lookup, cliConfig(), out, stdin)
	if err != nil {
		fmt.Fprintln(stderr, "kukel:", err)
		return 1
	}
	machine.SetStderr(stderr)
	p := vm.NewProfiler(0, cpu)
	machine.StartProfile(p, "main")
	code, err := machine.Run(progArgs)
	machine.StopProfile()
	out.Flush()
	if err != nil {
		fmt.Fprintln(stderr, err)
		code = 1
	}
	// The report also of a program that failed or called exit.
	if err := profile.Report(report, p); err != nil {
		fmt.Fprintln(stderr, "kukel:", err)
		return 1
	}
	return code
}
