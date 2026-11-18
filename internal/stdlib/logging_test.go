package stdlib_test

import (
	"bufio"
	"crypto/tls"
	"io"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/maarten-boot/kukel"
)

// runLog runs main of src and gives what it wrote to standard output and
// standard error; an error fails the test.
func runLog(t *testing.T, src string) (string, string) {
	t.Helper()
	p, err := kukel.CompileSource("t.kkl", src, kukel.CompileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var out, errs strings.Builder
	in, err := kukel.New(p, kukel.Options{Stdout: &out, Stderr: &errs})
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	if _, err := in.Run(); err != nil {
		t.Fatalf("%v\nstdout: %s\nstderr: %s", err, out.String(), errs.String())
	}
	return out.String(), errs.String()
}

// The records: levels, placeholders, escaping, handlers in turn with their
// own levels and bubble, processors, and the errors of wrong calls.
func TestLoggingRecords(t *testing.T) {
	out := run(t, `
use logging;
use time;

main() {
    local log = logging::get("shop");
    println(log == logging::get("shop"), log->channel(), log->level(), len(log->handlers()));
    local all = new logging::Memory({"formatter": new logging::Line("{channel}.{level}: {message} {context} {extra}")});
    local warn = new logging::Memory({"level": "warning", "bubble": false, "formatter": new logging::Line("{level} {message} {context}")});
    local never = new logging::Memory();
    log->handlers([warn, all, never]);
    log->info("order {id} paid by {user}, {missing} {not a name} {}", {"id": 1042, "user": "ann", "total": 99.5});
    log->debug("not made: the logger's level is info");
    log->level("DEBUG")->debug("now {x}", {"x": [1, {"a": nil}]});
    log->warning("evil\nshop.INFO: forged\r\t\x1b\x7f", {"v": "a\nb", "f": 1.0 / 0});
    log->log(3, "by number");
    log->log("critical", "{e} at {t}", {"e": new Error("boom", "ValueError"), "t": new time::Time(2026, 11, 16, 10, 0, 0)});
    log->processor(logging::pid)->processor(fn(r) { r["extra"]["id"] = 7; })->notice("with extra");

    for (local l in all->lines()) {
        println(l);
    }
    println("--");

    for (local l in warn->lines()) {
        println(l);
    }
    println(len(never->records()));
    local r = all->records()[0];
    println(keys(r), typeof(r["time"]), r["message"], r["context"]["id"]);
    println(all->records()[2]["extra"]["pid"] > 0, all->level(), warn->level());
    all->clear();
    println(len(all->lines()), logging::severity("Error"), logging::severity(7));

    for (local f in [
        fn() { log->log("loud", "x"); }, fn() { log->info("x", [1]); }, fn() { logging::get(""); },
        fn() { log->handler(5); }, fn() { log->processor("host"); }, fn() { new logging::Memory({"levle": "info"}); },
        fn() { new logging::Stderr({"formatter": 1}); }, fn() { new logging::RFC5424({"facility": "local8"}); },
        fn() { log->handlers(1); },
    ]) {
        try {
            f();
            println("no error");
        } catch (e) {
            println(e->kind, e->message);
        }
    }
}
`)
	want := `true shop info 1
shop.INFO: order 1042 paid by ann, {missing} {not a name} {} {"total":99.5}
shop.DEBUG: now [1,{"a":null}]
shop.NOTICE: with extra {"pid":PID,"id":7}
--
WARNING evil\nshop.INFO: forged\r\t\x1b\x7f {"v":"a\nb","f":"inf"}
ERROR by number
CRITICAL ValueError: boom at 2026-11-16T10:00:00Z
3
["time", "channel", "level", "message", "context", "extra"] object order {id} paid by {user}, {missing} {not a name} {} 1042
true debug warning
0 3 7
ValueError logging: not a level: loud (debug, info, notice, warning, error, critical, alert or emergency)
TypeError logging: the context is a map, not vector
TypeError logging::get: a channel is a string that is not empty, not ""
TypeError logging: a handler is an object of a class derived from logging::Handler, not 5
TypeError logging: a processor is a function, not string
ValueError logging::Memory: unknown option levle
TypeError logging: a formatter is an object of a class derived from logging::Formatter, not 1
ValueError logging::RFC5424: not a facility: local8 (user, daemon, local0 ... local7, ...)
TypeError logging: handlers takes a vector of handlers, not int
`
	want = strings.Replace(want, "PID", strconv.Itoa(os.Getpid()), 1)
	if out != want {
		t.Errorf("got:\n%s\nwant:\n%s", out, want)
	}
}

// The default: info and worse to standard error, as a line with the time
// in UTC to the millisecond; the JSON formatter; a handler of one's own.
func TestLoggingDefault(t *testing.T) {
	out, errs := runLog(t, `
use logging;
use json;

class Upper : logging::Handler {
    Upper();
    write(text);
}

Upper::Upper() {
    logging::Handler::Handler({"formatter": new logging::Line("{level} {message}")});
}

Upper::write(text) {
    println(upper(text));
}

main() {
    local log = logging::get("app");
    log->debug("hidden");
    log->info("started {n}", {"n": 1, "bad": "\xff"});
    local m = new logging::Memory({"formatter": new logging::JSON()});
    local e = nil;

    try {
        throw new Error("e");
    } catch (x) {
        e = x;
    }
    log->handlers([m, new Upper()])->warning("w {a}", {"a": "x\ny", "e": e, "bad": "\xff"});
    local j = json::decode(m->lines()[0]);
    println(keys(j), j["message"], j["context"]["e"]["kind"], j["context"]["e"]["place"], j["context"]["bad"]);
    println(len(m->lines()[0]) > 0, find(m->lines()[0], "\n") < 0);
}
`)
	if !regexp.MustCompile(`^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d\.\d{3}Z app\.INFO: started 1 \{"bad":"\x{FFFD}"\}\n$`).MatchString(errs) {
		t.Errorf("stderr = %q", errs)
	}
	want := "WARNING W X\\NY\n" +
		`["time", "channel", "level", "message", "context", "extra"] w x` + "\ny Error main (t.kkl:26) \uFFFD\ntrue true\n"
	if out != want {
		t.Errorf("got:\n%s\nwant:\n%s", out, want)
	}
}

var (
	rfc5424 = regexp.MustCompile(`^<(\d+)>1 (\S+) (\S+) (\S+) (\S+) (\S+) (-|\[.*\])(?: (.*))?$`)
	rfc3164 = regexp.MustCompile(`^<(\d+)>([A-Z][a-z]{2} [ 1-3]\d \d\d:\d\d:\d\d) (?:(\S+) )?([A-Za-z0-9_.-]{1,32})\[(\d+)\]: (.*)$`)
)

// sd reads the STRUCTURED-DATA of RFC 5424 into id -> names and values,
// undoing the escapes of \", \\ and \].
func sd(t *testing.T, s string) map[string][][2]string {
	t.Helper()
	r := map[string][][2]string{}
	if s == "-" {
		return r
	}
	for len(s) > 0 {
		if s[0] != '[' {
			t.Fatalf("SD: %q", s)
		}
		end := strings.IndexAny(s, " ]")
		id := s[1:end]
		s = s[end:]
		for s[0] == ' ' {
			eq := strings.IndexByte(s, '=')
			name := s[1:eq]
			if len(name) > 32 || strings.ContainsAny(name, `= ]"`) {
				t.Errorf("SD-NAME %q", name)
			}
			if s[eq+1] != '"' {
				t.Fatalf("SD value: %q", s)
			}
			var v strings.Builder
			i := eq + 2
			for ; s[i] != '"'; i++ {
				if s[i] == '\\' && strings.IndexByte(`"\]`, s[i+1]) >= 0 {
					i++
				} else if s[i] == ']' || s[i] == '"' {
					t.Fatalf("SD value not escaped: %q", s)
				}
				v.WriteByte(s[i])
			}
			r[id] = append(r[id], [2]string{name, v.String()})
			s = s[i+1:]
		}
		if s[0] != ']' {
			t.Fatalf("SD: %q", s)
		}
		s = s[1:]
	}
	return r
}

// The syslog formats, read back by parsers of the RFCs: the priority, the
// time, the host, the app and process id, the context as STRUCTURED-DATA
// with its escapes, and control characters escaped.
func TestLoggingSyslogFormats(t *testing.T) {
	out := run(t, `
use logging;

main() {
    local m = new logging::Memory();
    local log = logging::get("shop")->handlers([m])->level("debug");
    log->info("order {id} paid by {user}", {"id": 1042, "user": "ann"});
    log->error("odd\nvalues", {"q": "say \"hi\"", "b": "a\\b", "br": "x]y", "nl": "1\n2", "long name with = and ] in it, more than 32": 1});
    log->processor(logging::host)->debug("");
    local formats = [
        new logging::RFC5424(), new logging::RFC5424({"facility": "local0", "app": "my app"}), new logging::RFC3164(),
        new logging::RFC3164({"facility": "daemon", "app": "a/b", "hostname": false}),
    ];

    for (local f in formats) {
        for (local r in m->records()) {
            println(f->format(r));
        }
    }
}
`)
	host, _ := os.Hostname()
	pid := strconv.Itoa(os.Getpid())
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(lines) != 12 {
		t.Fatalf("%d lines:\n%s", len(lines), out)
	}
	type want struct {
		pri, app, msg string
		sd            map[string][][2]string
	}
	wants := []want{
		{"14", "shop", "order 1042 paid by ann", map[string][][2]string{"ctx@32473": {{"id", "1042"}, {"user", "ann"}}}},
		{"11", "shop", `odd\nvalues`, map[string][][2]string{"ctx@32473": {
			{"q", `say "hi"`}, {"b", `a\b`}, {"br", "x]y"}, {"nl", `1\n2`}, {"long_name_with___and___in_it,_mo", "1"},
		}}},
		{"15", "shop", "", map[string][][2]string{"extra@32473": {{"host", host}}}},
	}
	for i, l := range lines[:6] {
		w := wants[i%3]
		g := rfc5424.FindStringSubmatch(l)
		if g == nil {
			t.Errorf("not RFC 5424: %q", l)
			continue
		}
		if i >= 3 {
			n, _ := strconv.Atoi(w.pri)
			w.pri, w.app = strconv.Itoa(n-8+16*8), "my_app"
		}
		if _, err := time.Parse(time.RFC3339Nano, g[2]); err != nil || !strings.HasSuffix(g[2], "Z") || len(g[2]) != 24 {
			t.Errorf("time %q: %v", g[2], err)
		}
		if g[1] != w.pri || g[3] != host || g[4] != w.app || g[5] != pid || g[6] != "-" || g[8] != w.msg {
			t.Errorf("%q: got %q", l, g[1:])
		}
		if got := sd(t, g[7]); !equalSD(got, w.sd) {
			t.Errorf("%q: SD %v, want %v", l, got, w.sd)
		}
	}
	for i, l := range lines[6:] {
		g := rfc3164.FindStringSubmatch(l)
		if g == nil {
			t.Errorf("not RFC 3164: %q", l)
			continue
		}
		if _, err := time.Parse(time.Stamp, g[2]); err != nil {
			t.Errorf("time %q: %v", g[2], err)
		}
		wantHost, app, pri := host, "shop", wants[i%3].pri
		if i >= 3 {
			n, _ := strconv.Atoi(pri)
			wantHost, app, pri = "", "a_b", strconv.Itoa(n-8+3*8)
		}
		if g[1] != pri || g[3] != wantHost || g[4] != app || g[5] != pid {
			t.Errorf("%q: got %q", l, g[1:])
		}
	}
	if !strings.HasSuffix(lines[7], `]: odd\nvalues {"q":"say \"hi\"","b":"a\\b","br":"x]y","nl":"1\n2","long name with = and ] in it, more than 32":1}`) ||
		!strings.HasSuffix(lines[6], "]: order 1042 paid by ann") || !strings.HasSuffix(lines[8], `]:  {"host":"`+host+`"}`) {
		t.Errorf("RFC 3164 messages:\n%s", strings.Join(lines[6:9], "\n"))
	}
}

func equalSD(a, b map[string][][2]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		w := b[k]
		if len(v) != len(w) {
			return false
		}
		for i := range v {
			if v[i] != w[i] {
				return false
			}
		}
	}
	return true
}

// File: appends a line for each record; a file moved away (logrotate) or
// emptied (copytruncate) is opened again by path.
func TestLoggingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")
	src := strings.ReplaceAll(`
use logging;

main() {
    local f = new logging::File(PATH, {"formatter": new logging::Line("{message}")});
    local log = logging::get("f")->handlers([f]);
    log->info("one");
    log->info("two");
    println(f->path() == PATH);
    readline();
    log->info("three");
    readline();
    log->info("four");
    f->close();
    log->info("five");
}
`, "PATH", strconvQuote(path))
	p, err := kukel.CompileSource("t.kkl", src, kukel.CompileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	// The program waits for a line of standard input between the steps,
	// so that the test can move and empty the file at those points.
	r, w := io.Pipe()
	var out strings.Builder
	in, err := kukel.New(p, kukel.Options{Stdout: &out, Stdin: r})
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	done := make(chan error, 1)
	go func() { _, err := in.Run(); done <- err }()
	read := func(p string) string { b, _ := os.ReadFile(p); return string(b) }
	waitFor := func(s string) {
		t.Helper()
		for range 500 {
			if read(path) == s {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("the log is %q, not %q", read(path), s)
	}
	waitFor("one\ntwo\n")
	if err := os.Rename(path, path+".1"); err != nil {
		t.Fatal(err)
	}
	io.WriteString(w, "\n")
	waitFor("three\n")
	if err := os.Truncate(path, 0); err != nil {
		t.Fatal(err)
	}
	io.WriteString(w, "\n")
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := read(path + ".1"); got != "one\ntwo\n" {
		t.Errorf("moved file: %q", got)
	}
	if got := read(path); got != "four\nfive\n" {
		t.Errorf("file: %q", got)
	}
	if out.String() != "true\n" {
		t.Errorf("stdout %q", out.String())
	}
}

// Syslog over each transport, to servers of the test: the local socket
// gets RFC 3164 without the host, the others RFC 5424; TCP and TLS frame
// each record with its length (RFC 6587).
func TestLoggingSyslog(t *testing.T) {
	var addrs []string
	recv := map[string]chan string{}

	if runtime.GOOS != "windows" {
		sock := filepath.Join(t.TempDir(), "log.sock")
		uc, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: sock, Net: "unixgram"})
		if err != nil {
			t.Fatal(err)
		}
		defer uc.Close()
		addr := "unixgram://" + sock
		addrs = append(addrs, addr)
		recv[addr] = packets(uc)
	}
	udp, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer udp.Close()
	addrs = append(addrs, "udp://"+udp.LocalAddr().String())
	recv[addrs[len(addrs)-1]] = packets(udp)

	tcp, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer tcp.Close()
	addrs = append(addrs, "tcp://"+tcp.Addr().String())
	recv[addrs[len(addrs)-1]] = frames(t, tcp)

	p := newPKI(t)
	tl, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{p.pair}})
	if err != nil {
		t.Fatal(err)
	}
	defer tl.Close()
	addrs = append(addrs, "tls://localhost:"+portOf(tl.Addr().String()))
	recv[addrs[len(addrs)-1]] = frames(t, tl)

	var calls strings.Builder
	for _, a := range addrs {
		calls.WriteString("    send(" + strconvQuote(a) + ", " + strconvQuote(p.ca) + ");\n")
	}
	out := run(t, `
use logging;

send(address, ca) {
    local h = new logging::Syslog(address, {"ca": ca, "app": "kk"});
    local log = logging::get(address)->handlers([h]);
    log->info("one {n}", {"n": 1});
    log->warning("two\nlines");
    println(h->address() == address);
    h->close();
}

main() {
`+calls.String()+`}
`)
	if out != strings.Repeat("true\n", len(addrs)) {
		t.Errorf("stdout %q", out)
	}
	for _, a := range addrs {
		var ms []string
		for range 2 {
			select {
			case m := <-recv[a]:
				ms = append(ms, m)
			case <-time.After(10 * time.Second):
				t.Fatalf("%s: no record", a)
			}
		}
		if strings.HasPrefix(a, "unixgram:") {
			g0, g1 := rfc3164.FindStringSubmatch(ms[0]), rfc3164.FindStringSubmatch(ms[1])
			if g0 == nil || g1 == nil || g0[1] != "14" || g0[3] != "" || g0[4] != "kk" || g0[6] != "one 1" || g1[1] != "12" || g1[6] != `two\nlines` {
				t.Errorf("%s: %q", a, ms)
			}
			continue
		}
		g0, g1 := rfc5424.FindStringSubmatch(ms[0]), rfc5424.FindStringSubmatch(ms[1])
		if g0 == nil || g1 == nil || g0[1] != "14" || g0[4] != "kk" || g0[7] != `[ctx@32473 n="1"]` || g0[8] != "one 1" ||
			g1[1] != "12" || g1[7] != "-" || g1[8] != `two\nlines` {
			t.Errorf("%s: %q", a, ms)
		}
	}
}

// packets gives the datagrams that come to c.
func packets(c net.PacketConn) chan string {
	ch := make(chan string, 10)
	go func() {
		buf := make([]byte, 65536)
		for {
			n, _, err := c.ReadFrom(buf)
			if err != nil {
				return
			}
			ch <- string(buf[:n])
		}
	}()
	return ch
}

// frames gives the records of RFC 6587's octet counting that come over the
// connections to l.
func frames(t *testing.T, l net.Listener) chan string {
	ch := make(chan string, 10)
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				r := bufio.NewReader(c)
				for {
					n, err := r.ReadString(' ')
					if err != nil {
						return
					}
					size, err := strconv.Atoi(strings.TrimSuffix(n, " "))
					if err != nil {
						t.Errorf("frame length %q", n)
						return
					}
					b := make([]byte, size)
					if _, err := io.ReadFull(r, b); err != nil {
						t.Errorf("frame: %v", err)
						return
					}
					ch <- string(b)
				}
			}()
		}
	}()
	return ch
}

// A handler that fails: its records go to standard error, with a note
// once a minute that counts what was lost; the program goes on, and the
// handlers after it still get the records.
func TestLoggingFailure(t *testing.T) {
	addr := "tcp://127.0.0.1:1"
	if l, err := net.Listen("tcp", "127.0.0.1:0"); err == nil {
		addr = "tcp://" + l.Addr().String()
		l.Close()
	}
	out, errs := runLog(t, `
use logging;

main() {
    local m = new logging::Memory();
    local log = logging::get("s")->handlers([new logging::Syslog(`+strconvQuote(addr)+`, {"timeout": 2}), m]);
    log->error("one");
    log->error("two");
    println(len(m->records()));
}
`)
	if out != "2\n" {
		t.Errorf("stdout %q", out)
	}
	lines := strings.Split(strings.TrimSuffix(errs, "\n"), "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[0], "logging: logging::Syslog failed: NetError: ") ||
		!strings.HasSuffix(lines[0], "; its records go to standard error") ||
		!strings.HasSuffix(lines[1], " s.ERROR: one") || !strings.HasSuffix(lines[2], " s.ERROR: two") {
		t.Errorf("stderr:\n%s", errs)
	}
}

// configure: from a map, as TOML gives it; channels with their own level
// and handlers, shared handlers, loggers made before that follow, and an
// error that names the key and leaves the configuration as it was.
func TestLoggingConfigure(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "app.log")
	out, errs := runLog(t, strings.ReplaceAll(`
use logging;
use toml;

main() {
    local early = logging::get("shop");
    local c = toml::decode("
level = \"warning\"
processors = [\"pid\"]

[handler.mem]
type = \"memory\"
format = \"json\"

[handler.file]
type = \"file\"
line = \"{channel} {level} {message}\"
level = \"info\"

[channel.shop]
level = \"debug\"
handlers = [\"mem\", \"file\", \"stderr\"]
processors = []
");
    c["handler"]["file"]["path"] = PATH;
    logging::configure(c);
    early->debug("debug from shop");
    early->info("info {n}", {"n": 2});
    logging::get("db")->info("not shown");
    logging::get("db")->warning("db warning");
    local mem = early->handlers()[0];
    println(len(mem->lines()), early->level(), logging::get("db")->level(), logging::get("db")->processors()[0] == logging::pid);

    for (local c in [
        {"levle": 1}, {"level": "loud"}, {"handler": {"x": {"type": "smoke"}}}, {"handler": {"x": {}}},
        {"handler": {"x": {"type": "file"}}}, {"handler": {"x": {"type": "stderr", "adress": "x"}}},
        {"channel": {"a": {"handlers": ["nope"]}}}, {"handler": {"x": {"type": "stderr", "format": "xml"}}},
        {"handler": {"x": {"type": "syslog", "address": "http://x"}}}, {"processors": ["time"]},
        {"handler": {"x": {"type": "stderr", "facility": "local0"}}}, {"channel": {"a": {"leve": "info"}}},
        {"handler": {"x": {"type": "syslog", "facility": "local9"}}}, {"handler": {"x": {"type": "memory", "bubble": "no"}}}, 7,
    ]) {
        try {
            logging::configure(c);
            println("no error");
        } catch (e) {
            println(e->kind, e->message);
        }
    }
    early->info("still configured");
    println(len(mem->lines()));
    logging::configure({});
    early->debug("hidden");
    early->info("default");
    println(readfile(PATH));
}
`, "PATH", strconvQuote(path)))
	want := `2 debug warning true
ValueError logging::configure: unknown key levle
ValueError logging::configure: level: logging: not a level: loud (debug, info, notice, warning, error, critical, alert or emergency)
ValueError logging::configure: handler.x.type: smoke (stderr, file, syslog, memory or null)
ValueError logging::configure: handler.x.type: missing (stderr, file, syslog, memory or null)
ValueError logging::configure: handler.x.path: the path of the file is missing
ValueError logging::configure: handler.x: unknown key adress
ValueError logging::configure: channel.a.handlers: there is no handler nope
ValueError logging::configure: handler.x.format: xml (line, json, rfc5424 or rfc3164)
ValueError logging::configure: handler.x: logging::Syslog: not an address of a syslog: http://x (unixgram://, udp://, tcp:// or tls://)
ValueError logging::configure: processors: there is no processor time (host or pid)
ValueError logging::configure: handler.x.facility: only for syslog formats
ValueError logging::configure: channel.a: unknown key leve
ValueError logging::configure: handler.x: logging::RFC3164: not a facility: local9 (user, daemon, local0 ... local7, ...)
ValueError logging::configure: handler.x.bubble: true or false, not no
TypeError logging::configure: the configuration is a map, not int
3
shop INFO info 2
shop INFO still configured

`
	if out != want {
		t.Errorf("got:\n%s\nwant:\n%s", out, want)
	}
	lines := strings.Split(strings.TrimSuffix(errs, "\n"), "\n")
	if len(lines) != 5 || !strings.HasSuffix(lines[0], " shop.DEBUG: debug from shop") || !strings.HasSuffix(lines[1], ` shop.INFO: info 2`) ||
		!strings.HasSuffix(lines[2], ` db.WARNING: db warning {"pid":`+strconv.Itoa(os.Getpid())+`}`) ||
		!strings.HasSuffix(lines[3], " shop.INFO: still configured") || !strings.HasSuffix(lines[4], " shop.INFO: default") {
		t.Errorf("stderr:\n%s", errs)
	}
}

// logging uses net, fs, json, time, os, re and utf8: a program that may
// not use one of them cannot use logging.
func TestLoggingModules(t *testing.T) {
	src := "use logging;\nmain() { logging::get(\"a\")->info(\"x\"); }\n"
	_, err := kukel.CompileSource("t.kkl", src, kukel.CompileOptions{StandardModules: []string{"fs", "json", "time", "os", "re", "utf8"}})
	if err == nil || !strings.Contains(err.Error(), "net") {
		t.Errorf("without net: %v", err)
	}
	if _, err := kukel.CompileSource("t.kkl", src, kukel.CompileOptions{StandardModules: []string{"net", "fs", "json", "time", "os", "re", "utf8"}}); err != nil {
		t.Errorf("with all it needs: %v", err)
	}
}

// A call below the level costs little: no record is made. f calls debug
// n times, g calls an empty function as often, for comparison.
func BenchmarkLoggingBelowLevel(b *testing.B) {
	p, err := kukel.CompileSource("t.kkl", `
use logging;
global lg = logging::get("bench");
f(n) {
    for (local i = 0; i < n; i++) {
        lg->debug("x");
    }
}
nothing(m) {}
g(n) {
    for (local i = 0; i < n; i++) {
        nothing("x");
    }
}
main() {}
`, kukel.CompileOptions{})
	if err != nil {
		b.Fatal(err)
	}
	in, err := kukel.New(p, kukel.Options{})
	if err != nil {
		b.Fatal(err)
	}
	defer in.Close()
	for _, fn := range []string{"f", "g"} {
		b.Run(fn, func(b *testing.B) {
			if _, err := in.Call(fn, kukel.Int(int64(b.N))); err != nil {
				b.Fatal(err)
			}
		})
	}
}
