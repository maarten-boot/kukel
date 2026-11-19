package stdlib_test

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/maarten-boot/kukel"
)

// The tests of proc start this test binary as the child, so that they need
// no sh, cat or sleep and run on Windows too (the os/exec tests' helper
// process): with KUKEL_PROC_HELPER=1 in its environment it does what its
// arguments say, one after the other, instead of running the tests.
func TestMain(m *testing.M) {
	if os.Getenv("KUKEL_PROC_HELPER") == "1" {
		os.Exit(helper(os.Args[1:]))
	}
	os.Exit(m.Run())
}

// helper does the actions in args: out TEXT and err TEXT (a line to
// standard output or error), cat (standard input to output), env NAME
// (the variable's value, or <unset>), pwd, args (each argument after it
// in brackets, then stops), flood (writes without end), sleep SECONDS,
// spawn SECONDS (starts a copy of itself that sleeps, and prints its
// pid), kill (ends itself with TERM), exit CODE.
func helper(args []string) int {
	for i := 0; i < len(args); i++ {
		next := func() string { i++; return args[i] }
		switch args[i] {
		case "out":
			fmt.Println(next())
		case "err":
			fmt.Fprintln(os.Stderr, next())
		case "cat":
			b, _ := readAll(os.Stdin)
			os.Stdout.Write(b)
		case "env":
			if v, ok := os.LookupEnv(next()); ok {
				fmt.Println(v)
			} else {
				fmt.Println("<unset>")
			}
		case "pwd":
			d, _ := os.Getwd()
			fmt.Println(d)
		case "args":
			for _, a := range args[i+1:] {
				fmt.Printf("[%s]", a)
			}
			fmt.Println()
			return 0
		case "flood":
			line := []byte(strings.Repeat("x", 1023) + "\n")
			for {
				if _, err := os.Stdout.Write(line); err != nil {
					return 1
				}
			}
		case "sleep":
			s, _ := strconv.ParseFloat(next(), 64)
			time.Sleep(time.Duration(s * float64(time.Second)))
		case "spawn":
			c := exec.Command(os.Args[0], "sleep", next())
			c.Env = os.Environ()
			c.Start()
			fmt.Println(c.Process.Pid)
		case "kill":
			p, _ := os.FindProcess(os.Getpid())
			p.Signal(os.Kill)
			time.Sleep(time.Second)
		case "exit":
			n, _ := strconv.Atoi(next())
			return n
		}
	}
	return 0
}

func readAll(f *os.File) ([]byte, error) {
	var b []byte
	buf := make([]byte, 4096)
	for {
		n, err := f.Read(buf)
		b = append(b, buf[:n]...)
		if err != nil {
			return b, nil
		}
	}
}

// runProc runs src with every standard module, process too; @HELPER@ in it
// is the helper's path (a Kukel string), and @HENV@ the environment that makes it one.
func runProc(t *testing.T, src string) (string, error) {
	t.Helper()
	src = strings.ReplaceAll(src, "@HELPER@", strconv.Quote(os.Args[0]))
	src = strings.ReplaceAll(src, "@HENV@", `{"KUKEL_PROC_HELPER": "1"}`)
	p, err := kukel.CompileSource("t.kkl", src, kukel.CompileOptions{StandardModules: kukel.StandardModules()})
	if err != nil {
		t.Fatal(err)
	}
	var out, errOut strings.Builder
	in, err := kukel.New(p, kukel.Options{Stdout: &out, Stderr: &errOut})
	if err != nil {
		t.Fatal(err)
	}
	_, err = in.Run()
	return out.String() + errOut.String(), err
}

func TestProcRun(t *testing.T) {
	out, err := runProc(t, `use proc;
h(args..) { return [@HELPER@, args..]; }
main() {
    local o = {"env": @HENV@};
    local r = proc::run(h("out", "hello", "err", "to err"), o);
    println(r["code"], r["signal"], typeof(r["out"]), r["out"] == "hello\n", r["err"], typeof(r["time"]), r["timedout"]);
    println(proc::run(h("args", "a b", "$HOME", "*", "x;y", "'q'", "\"d\"", "line\nbreak", ""), o)["out"]);
    println(proc::run(h("cat"), {"env": @HENV@, "input": "fed\nin\n"})["out"]);
    println(proc::run(h("cat"), o)["out"] == "", "(no input)");
    local both = proc::run(h("out", "1", "err", "2", "out", "3"), {"env": @HENV@, "err": "out"});
    println(both["out"] == "1\n2\n3\n" || both["out"] == "1\n3\n2\n", both["err"]);
    local sep = proc::run(h("out", "o", "err", "e"), {"env": @HENV@, "err": "capture"});
    println(sep["out"], sep["err"]);
    println(proc::run(h("out", "gone"), {"env": @HENV@, "out": "null"})["out"]);
}`)
	want := `0 nil string true nil float false
[a b][$HOME][*][x;y]['q']["d"][line
break][]

fed
in

true (no input)
true nil
o
 e

nil
to err
`
	if err != nil || out != want {
		t.Errorf("got %v:\n%s\nwant:\n%s", err, out, want)
	}
}

func TestProcFiles(t *testing.T) {
	dir := t.TempDir()
	f := strings.ReplaceAll(dir+"/log.txt", `\`, `/`)
	out, err := runProc(t, `use proc;
main() {
    proc::run([@HELPER@, "out", "one"], {"env": @HENV@, "out": {"file": "`+f+`"}});
    proc::run([@HELPER@, "out", "two", "err", "three"], {"env": @HENV@, "out": {"append": "`+f+`"}, "err": "out"});
    println(readfile("`+f+`"));
    println(proc::run([@HELPER@, "pwd"], {"env": @HENV@, "cwd": "`+strings.ReplaceAll(dir, `\`, `/`)+`"})["out"] != "");
}`)
	if err != nil || out != "one\ntwo\nthree\n\ntrue\n" {
		t.Errorf("got %v:\n%s", err, out)
	}
	wd, _ := runProc(t, `use proc; main() { print(proc::run([@HELPER@, "pwd"], {"env": @HENV@, "cwd": `+strconv.Quote(dir)+`})["out"]); }`)
	if strings.TrimSpace(wd) != dir {
		t.Errorf("pwd in %s: %q", dir, wd)
	}
}

func TestProcEnv(t *testing.T) {
	t.Setenv("KUKEL_PROC_SEEN", "parent")
	out, err := runProc(t, `use proc;
main() {
    local e = {"KUKEL_PROC_HELPER": "1", "EXTRA": "x=y"};
    println(proc::run([@HELPER@, "env", "EXTRA", "env", "KUKEL_PROC_SEEN"], {"env": e})["out"]);
    e["KUKEL_PROC_SEEN"] = nil;
    println(proc::run([@HELPER@, "env", "KUKEL_PROC_SEEN"], {"env": e})["out"]);
    println(proc::run([@HELPER@, "env", "PATH", "env", "EXTRA"], {"env": e, "clearenv": true})["out"]);
    println(proc::env()["KUKEL_PROC_SEEN"], proc::which("no-such-program-x"));
}`)
	want := "x=y\nparent\n\n<unset>\n\n<unset>\nx=y\n\nparent nil\n"
	if err != nil || out != want {
		t.Errorf("got %v:\n%s\nwant:\n%s", err, out, want)
	}
}

func TestProcFailures(t *testing.T) {
	out, err := runProc(t, `use proc;
main() {
    local o = {"env": @HENV@, "err": "capture"};
    try {
        proc::run([@HELPER@, "err", "first", "err", "fatal: not here", "exit", "3"], o);
    } catch (e) {
        println(e->kind, e->code, e->signal, e->err == "first\nfatal: not here\n", len(e->command));
        println(e->message == "proc::run: " + str(e->command) + " exited with 3: fatal: not here");
    }
    local r = proc::run([@HELPER@, "exit", "4"], {"env": @HENV@, "check": false});
    println(r["code"], proc::exitcode(r));
    try {
        proc::run(["no-such-program-x", "a"]);
    } catch (e) {
        println(e->kind, e->message, e->code, e->command);
    }
    try {
        proc::run([@HELPER@, "sleep", "30"], {"env": @HENV@, "timeout": 0.3});
    } catch (e) {
        println(e->kind, find(e->message, "timed out after 0.3 s") > 0);
    }
    local t = proc::run([@HELPER@, "sleep", "30"], {"env": @HENV@, "timeout": 0.3, "check": false});
    println(t["timedout"], t["time"] < 10);
    try {
        proc::run([@HELPER@, "flood"], {"env": @HENV@, "max": 10000});
    } catch (e) {
        println(e->kind, find(e->message, "more than the option max allows") > 0);
    }
}`)
	want := `ProcError 3 nil true 7
true
4 4
ProcError proc::run: program no-such-program-x not found nil ["no-such-program-x", "a"]
ProcError true
true true
ProcError true
`
	if err != nil || out != want {
		t.Errorf("got %v:\n%s\nwant:\n%s", err, out, want)
	}
}

func TestProcSignals(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no signals on Windows")
	}
	out, err := runProc(t, `use proc;
main() {
    local r = proc::run([@HELPER@, "kill"], {"env": @HENV@, "check": false});
    println(r["code"], r["signal"], proc::exitcode(r));
    try {
        proc::run([@HELPER@, "kill"], {"env": @HENV@});
    } catch (e) {
        println(e->message == "proc::run: " + str(e->command) + " was ended by signal KILL", e->code, e->signal);
    }
}`)
	if err != nil || out != "nil KILL 137\ntrue nil KILL\n" {
		t.Errorf("got %v:\n%s", err, out)
	}
}

// A time limit ends what the child started, too: the grandchild's pid is
// gone afterwards.
func TestProcGroup(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows has no process groups that a signal reaches")
	}
	out, err := runProc(t, `use proc;
main() {
    local r = proc::run([@HELPER@, "spawn", "60", "sleep", "60"], {"env": @HENV@, "timeout": 0.5, "check": false});
    print(r["out"]);
}`)
	pid, perr := strconv.Atoi(strings.TrimSpace(out))
	if err != nil || perr != nil {
		t.Fatalf("got %v %v: %q", err, perr, out)
	}
	for end := time.Now().Add(5 * time.Second); ; time.Sleep(50 * time.Millisecond) {
		if p, err := os.FindProcess(pid); err != nil || p.Signal(syscallZero) != nil {
			break // gone
		}
		if time.Now().After(end) {
			p, _ := os.FindProcess(pid)
			p.Kill()
			t.Fatalf("the grandchild %d still runs", pid)
		}
	}
}

// syscallZero checks whether a process is there without signalling it.
var syscallZero = syscall.Signal(0)

func TestProcErrors(t *testing.T) {
	cases := map[string]string{
		`proc::run("ls")`:                                       `TypeError: proc::run: the command is a vector of strings (the program and its arguments), not "ls"`,
		`proc::run([])`:                                         `TypeError: proc::run: the command is a vector of strings (the program and its arguments), not []`,
		`proc::run(["ls", 1])`:                                  `TypeError: proc::run: argument 2 of the command is a int, not a string`,
		`proc::run(["ls"], [])`:                                 `TypeError: proc::run: the options are a map, not vector`,
		`proc::run(["ls"], {"bogus": 1})`:                       `ValueError: proc::run: unknown option "bogus" (input, stdin, out, err, cwd, env, clearenv, timeout, check, max)`,
		`proc::run(["ls"], {"out": "out"})`:                     `ValueError: proc::run: option out is "capture", "inherit", "null", {"file": path} or {"append": path}, not "out"`,
		`proc::run(["ls"], {"input": "x", "stdin": "inherit"})`: `ValueError: proc::run: options input and stdin together`,
		`proc::run(["ls"], {"env": {"A=B": "1"}})`:              `ValueError: proc::run: not a name for an environment variable: "A=B"`,
		`proc::run(["ls"], {"check": 1})`:                       `TypeError: proc::run: option check is true or false, not 1`,
		`proc::run(["ls"], {"timeout": -1})`:                    `ValueError: process::run: option timeout: -1 seconds (more than 0)`,
		`proc::run(["a\0b"])`:                                   `ValueError: process::run: argument 1 of the command holds a NUL byte`,
		`proc::exitcode(1)`:                                     `TypeError: proc::exitcode: a result of proc::run, not int`,
	}
	for call, want := range cases {
		out, err := runProc(t, "use proc; main() { "+call+"; }")
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: %v (output %q), want %s", call, err, out, want)
		}
	}
}

// F-74: a Go program gets proc only when it names process.
func TestProcOptIn(t *testing.T) {
	for _, mods := range [][]string{nil, {"json"}} {
		_, err := kukel.CompileSource("t.kkl", "use proc; main() { proc::run([\"ls\"]); }", kukel.CompileOptions{StandardModules: mods})
		if err == nil || !strings.Contains(err.Error(), "process") {
			t.Errorf("StandardModules %v: %v", mods, err)
		}
	}
	if _, err := kukel.CompileSource("t.kkl", "use proc; main() {}", kukel.CompileOptions{StandardModules: []string{"process"}}); err != nil {
		t.Errorf("StandardModules [process]: %v", err)
	}
}
