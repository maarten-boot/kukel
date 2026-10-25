package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"sync"
	"time"

	"github.com/maarten-boot/kukel/internal/builtins"
	"github.com/maarten-boot/kukel/internal/profile"
	"github.com/maarten-boot/kukel/internal/vm"
)

const profileUsage = "kukel profile [-o report.txt] [-pprof prof.pb.gz] [-cpu] a.kkl ... [-- args]"

// kukel profile (TODO.md Q78): runs a program as kukel run does, profiled
// (the main interpreter and its workers), then writes the report: to
// standard error, or to the file of -o; -pprof also writes the profile in
// Go's pprof format (for go tool pprof). -cpu leaves waiting out. Ctrl-C
// (SIGINT) stops the program; the report is written, and kukel ends as
// interrupted.
func cmdProfile(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	files, progArgs := args, []string(nil)
	for i, a := range args {
		if a == "--" {
			files, progArgs = args[:i], args[i+1:]
			break
		}
	}
	var reportFile, pprofFile string
	cpu := false
	for len(files) > 0 && strings.HasPrefix(files[0], "-") && files[0] != "-" {
		switch files[0] {
		case "-cpu":
			cpu = true
			files = files[1:]
		case "-o", "-pprof":
			if len(files) < 2 {
				fmt.Fprintf(stderr, "kukel profile: %s needs a file\nusage: %s\n", files[0], profileUsage)
				return 2
			}
			if files[0] == "-o" {
				reportFile = files[1]
			} else {
				pprofFile = files[1]
			}
			files = files[2:]
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
	for _, f := range files {
		if strings.HasSuffix(f, ".kkc") {
			fmt.Fprintf(stderr, "kukel profile: %s is a compiled program: profile its source files (kukel exec runs it)\n", f)
			return 2
		}
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
			fmt.Fprintln(stderr, toolError(err))
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

	// The report, once: after the run, or on Ctrl-C.
	var once sync.Once
	reportCode := 0
	writeReport := func() int {
		once.Do(func() {
			if err := profile.Report(report, p, nil); err != nil {
				fmt.Fprintln(stderr, "kukel:", err)
				reportCode = 1
				return
			}
			if pprofFile != "" {
				f, err := os.Create(pprofFile)
				if err == nil {
					err = profile.Pprof(f, p)
					if cerr := f.Close(); err == nil {
						err = cerr
					}
				}
				if err != nil {
					fmt.Fprintln(stderr, toolError(err))
					reportCode = 1
				}
			}
		})
		return reportCode
	}

	// Ctrl-C cancels the program (and its workers), so that the report
	// comes. A program that does not stop within a second (it waits in a Go
	// function that cannot be cancelled) gets its report anyway, while it
	// runs.
	cancel := machine.Cancellable()
	signals := make(chan os.Signal, 1)
	notifyInterrupt(signals)
	defer stopInterrupt(signals)
	ran := make(chan struct{}) // closed when the program ended
	interrupted := make(chan struct{})
	go func() {
		select {
		case <-signals:
		case <-ran:
			return
		}
		close(interrupted)
		cancel.Cancel(errProfileInterrupted)
		select {
		case <-ran:
		case <-time.After(time.Second):
			writeReport() // (not out.Flush: the program may write to it)
			exitProcess(dieInterrupted())
		}
	}()

	machine.StartProfile(p, "main")
	code, err := machine.Run(progArgs)
	close(ran)
	machine.StopProfile()
	out.Flush()
	select {
	case <-interrupted:
		writeReport()
		return dieInterrupted()
	default:
	}
	if err != nil {
		fmt.Fprintln(stderr, err)
		code = 1
	}
	// The report also of a program that failed or called exit.
	if writeReport() != 0 {
		return 1
	}
	return code
}

var errProfileInterrupted = errors.New("interrupted (Ctrl-C)")

// notifyInterrupt and stopInterrupt start and stop sending Ctrl-C (SIGINT)
// to c (a test sends its own). Where SIGINT is ignored (a program started
// in the background by a shell), it stays so.
var (
	notifyInterrupt = func(c chan<- os.Signal) {
		if !signal.Ignored(os.Interrupt) {
			signal.Notify(c, os.Interrupt)
		}
	}
	stopInterrupt = func(c chan<- os.Signal) { signal.Stop(c) }
)

// exitProcess ends kukel (a test records the code).
var exitProcess = os.Exit

// dieInterrupted ends kukel as Ctrl-C does when nothing catches it: by
// the signal, so that a shell sees an interrupted program (exit status
// 130); where it cannot, it gives that exit code.
var dieInterrupted = func() int {
	signal.Reset(os.Interrupt)
	if self, err := os.FindProcess(os.Getpid()); err == nil && self.Signal(os.Interrupt) == nil {
		time.Sleep(time.Second) // the signal ends the process meanwhile
	}
	return 130
}
