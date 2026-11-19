package processmod

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/maarten-boot/kukel/internal/native"
	"github.com/maarten-boot/kukel/internal/value"
)

// Grace is how long a process that a time limit or a cancellation ends
// gets after TERM before it is killed.
var Grace = 2 * time.Second

// A job is one program, or the programs of a pipeline, started together:
// in one process group of their own (unless the first reads an inherited
// standard input, which a terminal gives only to the foreground group),
// so that ending the job ends what they started in turn.
type job struct {
	argvs   [][]string
	cmds    []*exec.Cmd
	o       options
	grouped bool
	pgid    int // the group's id: the first program's pid

	closeAfterStart []*os.File // the children's ends of pipes, and opened files
	closeAtEnd      []*os.File // files opened for out and err
	stdin           *os.File   // spawn's pipes: the parent's ends
	stdout, stderr  *os.File
	outCap, errCap  *capture
	over            *overflow

	started   time.Time
	done      chan struct{} // closed when every program has ended
	errs      []error       // each program's Wait
	elapsed   float64
	timedout  atomic.Bool
	truncated atomic.Bool
	stopOnce  sync.Once
}

// live holds the jobs that run, for StopAll.
var live = struct {
	sync.Mutex
	m map[*job]bool
}{m: map[*job]bool{}}

// stopping is set by StopAll: kukel is about to end by a signal, so a wait
// whose processes it ended waits for that instead of reporting a failure.
var stopping atomic.Bool

// StopAll ends every process the module started that still runs, with
// what it started in turn: TERM, and KILL to what is left after Grace.
// kukel run calls it when it gets Ctrl-C, TERM or HUP, before it ends as
// the signal would end it: a child in a group of its own does not get the
// terminal's Ctrl-C.
func StopAll() {
	stopping.Store(true)
	live.Lock()
	jobs := make([]*job, 0, len(live.m))
	for j := range live.m {
		jobs = append(jobs, j)
		signalJob(j, "TERM")
	}
	live.Unlock()
	for end := time.Now().Add(Grace); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
		live.Lock()
		n := len(live.m)
		live.Unlock()
		if n == 0 {
			break
		}
	}
	for _, j := range jobs {
		signalJob(j, "KILL")
	}
}

// overflow tells, once, that a capture went over its cap.
type overflow struct {
	once sync.Once
	ch   chan struct{}
}

func (o *overflow) trip() { o.once.Do(func() { close(o.ch) }) }

// capture is a writer that keeps at most max bytes; more trips over. The
// programs of a pipeline write their errors to one capture at once.
type capture struct {
	mu   sync.Mutex
	b    bytes.Buffer
	max  int
	over *overflow
}

func (c *capture) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if room := c.max - c.b.Len(); len(p) > room {
		c.b.Write(p[:max(room, 0)])
		c.over.trip()
		return len(p), nil // the rest is dropped; the job is ended
	}
	return c.b.Write(p)
}

func (c *capture) value() value.Value {
	if c == nil {
		return value.NilValue
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return value.MakeString(c.b.String())
}

// newJob sets the programs of argvs up with the options; a file that
// cannot be opened gives run's map for that, as a program that cannot be
// started does.
func newJob(env value.Env, argvs [][]string, o options) (*job, value.Value) {
	j := &job{argvs: argvs, o: o, over: &overflow{ch: make(chan struct{})}, done: make(chan struct{})}
	fail := func(err error) (*job, value.Value) {
		j.closeAll()
		return nil, notStarted(argvs[0], err)
	}
	term, termOut := native.Terminal(env)
	for _, argv := range argvs {
		cmd := exec.Command(argv[0], argv[1:]...)
		cmd.Dir = o.dir
		if o.hasEnv {
			cmd.Env = o.env
		}
		cmd.WaitDelay = Grace // pipes a grandchild keeps open do not hold the wait
		j.cmds = append(j.cmds, cmd)
	}
	first, last := j.cmds[0], j.cmds[len(j.cmds)-1]
	switch o.in {
	case "data":
		first.Stdin = strings.NewReader(o.data)
	case "inherit":
		if term != nil && env.Stdin().Buffered() == 0 {
			first.Stdin = term
		} else {
			first.Stdin = env.Stdin()
		}
	case "pipe":
		r, w, err := os.Pipe()
		if err != nil {
			return fail(err)
		}
		first.Stdin, j.stdin = r, w
		j.closeAfterStart = append(j.closeAfterStart, r)
	}
	for i := 0; i+1 < len(j.cmds); i++ { // the pipeline's pipes
		r, w, err := os.Pipe()
		if err != nil {
			return fail(err)
		}
		j.cmds[i].Stdout, j.cmds[i+1].Stdin = w, r
		j.closeAfterStart = append(j.closeAfterStart, r, w)
	}
	sink := func(s stream, std io.Writer, isErr bool) (io.Writer, *os.File, error) {
		switch s.kind {
		case "capture":
			c := &capture{max: o.max, over: j.over}
			if isErr {
				j.errCap = c
			} else {
				j.outCap = c
			}
			return c, nil, nil
		case "inherit":
			if !isErr && termOut != nil {
				return termOut, nil, nil
			}
			return std, nil, nil
		case "null":
			return nil, nil, nil
		case "pipe":
			r, w, err := os.Pipe()
			if err != nil {
				return nil, nil, err
			}
			j.closeAfterStart = append(j.closeAfterStart, w)
			return w, r, nil
		}
		flags := os.O_WRONLY | os.O_CREATE | os.O_TRUNC
		if s.kind == "append" {
			flags = os.O_WRONLY | os.O_CREATE | os.O_APPEND
		}
		f, err := os.OpenFile(s.path, flags, 0o644)
		if err != nil {
			return nil, nil, err
		}
		j.closeAtEnd = append(j.closeAtEnd, f)
		return f, nil, nil
	}
	w, parent, err := sink(o.out, env.Stdout(), false)
	if err != nil {
		return fail(err)
	}
	last.Stdout, j.stdout = w, parent
	if o.err.kind != "out" {
		w, parent, err := sink(o.err, env.Stderr(), true)
		if err != nil {
			return fail(err)
		}
		j.stderr = parent
		for _, cmd := range j.cmds {
			cmd.Stderr = w
		}
	} else {
		for _, cmd := range j.cmds { // each program's errors go where its output goes
			cmd.Stderr = cmd.Stdout
		}
	}
	j.grouped = o.in != "inherit"
	return j, value.NilValue
}

// start starts the programs; one that cannot be started ends those
// started before it, and gives run's map for that.
func (j *job) start() value.Value {
	j.started = time.Now()
	for i, cmd := range j.cmds {
		setGroup(cmd, j.grouped, j.pgid)
		if err := cmd.Start(); err != nil {
			for _, c := range j.cmds[:i] {
				c.Process.Kill()
				c.Wait()
			}
			j.closeAll()
			return notStarted(j.argvs[i], err)
		}
		if i == 0 {
			j.pgid = cmd.Process.Pid
		}
	}
	for _, f := range j.closeAfterStart { // the children have them now
		f.Close()
	}
	j.closeAfterStart = nil
	j.errs = make([]error, len(j.cmds))
	live.Lock()
	live.m[j] = true
	live.Unlock()
	go func() {
		for i, cmd := range j.cmds {
			j.errs[i] = cmd.Wait()
		}
		j.elapsed = time.Since(j.started).Seconds()
		for _, f := range j.closeAtEnd {
			f.Close()
		}
		live.Lock()
		delete(live.m, j)
		live.Unlock()
		close(j.done)
	}()
	go j.watch()
	return value.NilValue
}

// watch ends the job when its time limit passes or a capture goes over
// its cap.
func (j *job) watch() {
	var limit <-chan time.Time
	if j.o.limit > 0 {
		t := time.NewTimer(time.Duration(j.o.limit * float64(time.Second)))
		defer t.Stop()
		limit = t.C
	}
	select {
	case <-j.done:
	case <-limit:
		j.timedout.Store(true)
		j.stop()
	case <-j.over.ch:
		j.truncated.Store(true)
		j.stop()
	}
}

// stop ends the job, once: TERM, then KILL after Grace (and to what the
// group left running once its programs ended). It does not wait.
func (j *job) stop() {
	j.stopOnce.Do(func() {
		signalJob(j, "TERM")
		go func() {
			select {
			case <-j.done:
			case <-time.After(Grace):
			}
			signalJob(j, "KILL")
		}()
	})
}

// wait waits for the job's end, at most timeout (nil: as long as it
// takes). finished is false when the timeout passed first. When the
// program is cancelled meanwhile, the job is ended and waited for, and the
// cancellation is the error.
func (j *job) wait(env value.Env, timeout *time.Duration) (finished bool, err error) {
	var limit <-chan time.Time
	if timeout != nil {
		t := time.NewTimer(*timeout)
		defer t.Stop()
		limit = t.C
	}
	select {
	case <-j.done:
		finished = true
	case <-limit:
	case <-env.Done():
		j.stop()
		<-j.done
		if e := env.Err(); e != nil {
			return true, e
		}
		finished = true
	}
	if finished && stopping.Load() {
		select {} // kukel ends by the signal that made StopAll run
	}
	return finished, nil
}

// result gives run's map for the job, which has ended.
func (j *job) result() value.Value {
	r := value.NewMap()
	set := func(k string, v value.Value) { r.Set(value.MakeString(k), v) }
	set("started", value.True)
	n := len(j.cmds)
	codes, signals := make([]value.Value, n), make([]value.Value, n)
	var signum value.Value
	for i, cmd := range j.cmds {
		codes[i], signals[i], signum = exitOf(cmd, j.errs[i])
	}
	set("code", codes[n-1])
	set("signal", signals[n-1])
	set("signum", signum)
	set("out", j.outCap.value())
	set("err", j.errCap.value())
	set("time", value.MakeFloat(j.elapsed))
	set("timedout", value.MakeBool(j.timedout.Load()))
	set("truncated", value.MakeBool(j.truncated.Load()))
	if n > 1 {
		set("codes", value.MakeVector(codes))
		set("signals", value.MakeVector(signals))
	}
	return value.MakeMap(r)
}

// closeAll closes the files a job that did not start had opened.
func (j *job) closeAll() {
	for _, fs := range [][]*os.File{j.closeAfterStart, j.closeAtEnd, {j.stdin, j.stdout, j.stderr}} {
		for _, f := range fs {
			if f != nil {
				f.Close()
			}
		}
	}
}
