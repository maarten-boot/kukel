package stdlib_test

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"io"
	"math/big"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/mail"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/maarten-boot/kukel"
)

// run runs main of src and gives what it printed; an error fails the test.
func run(t *testing.T, src string) string {
	t.Helper()
	out, err := tryRun(t, src)
	if err != nil {
		t.Fatalf("%v\noutput: %s", err, out)
	}
	return out
}

func tryRun(t *testing.T, src string) (string, error) {
	t.Helper()
	p, err := kukel.CompileSource("t.kkl", src, kukel.CompileOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	in, err := kukel.New(p, kukel.Options{Stdout: &out})
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	_, err = in.Run()
	return out.String(), err
}

// part is a leaf of a parsed message: its content type, disposition
// parameters, Content-ID and decoded body.
type part struct {
	typ, filename, cid, disposition string
	body                            string
}

// parse reads a message as Go's net/mail and mime/multipart do, checking
// the rules a sender must keep on the way, and gives its headers and
// leaves in order, with the multipart structure as "mixed(alternative(...
func parse(t *testing.T, raw string) (*mail.Message, []part, string) {
	t.Helper()
	for i, l := range strings.Split(raw, "\r\n") {
		if len(l) > 78 {
			t.Errorf("line %d is %d bytes: %q", i+1, len(l), l)
		}
		if strings.ContainsAny(l, "\r\n") {
			t.Errorf("line %d has a bare line ending: %q", i+1, l)
		}
	}
	if !strings.HasSuffix(raw, "\r\n") {
		t.Errorf("the message does not end with CRLF")
	}
	m, err := mail.ReadMessage(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	var parts []part
	var walk func(h map[string][]string, body io.Reader) string
	walk = func(h map[string][]string, body io.Reader) string {
		get := func(k string) string {
			if v := h[k]; len(v) > 0 {
				return v[0]
			}
			return ""
		}
		mt, params, err := mime.ParseMediaType(get("Content-Type"))
		if err != nil {
			t.Fatalf("Content-Type %q: %v", get("Content-Type"), err)
		}
		if strings.HasPrefix(mt, "multipart/") {
			r := multipart.NewReader(body, params["boundary"])
			var subs []string
			for {
				p, err := r.NextRawPart()
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatal(err)
				}
				subs = append(subs, walk(p.Header, p))
			}
			return strings.TrimPrefix(mt, "multipart/") + "(" + strings.Join(subs, " ") + ")"
		}
		var data []byte
		switch cte := get("Content-Transfer-Encoding"); cte {
		case "quoted-printable":
			data, err = io.ReadAll(quotedprintable.NewReader(body))
		case "base64":
			data, err = io.ReadAll(base64.NewDecoder(base64.StdEncoding, body))
		case "7bit":
			data, err = io.ReadAll(body)
		default:
			t.Fatalf("Content-Transfer-Encoding %q", cte)
		}
		if err != nil {
			t.Fatal(err)
		}
		p := part{typ: mt, body: string(data), cid: get("Content-Id")}
		if d := get("Content-Disposition"); d != "" {
			disp, dp, err := mime.ParseMediaType(d)
			if err != nil {
				t.Fatalf("Content-Disposition %q: %v", d, err)
			}
			p.disposition, p.filename = disp, dp["filename"]
		}
		parts = append(parts, p)
		return mt
	}
	shape := walk(m.Header, m.Body)
	return m, parts, shape
}

// The messages mail writes, read back by Go: headers (encoded words,
// addresses), the MIME structure for each combination of bodies and
// parts, quoted-printable and base64 bodies, file names.
func TestMailMessages(t *testing.T) {
	out := run(t, `
use mail;
use time;

main() {
    local long = "";

    for (local i = 0; i < 30; i++) {
        long += "word" + str(i) + " ";
    }
    local ms = [
        new mail::Message()->from("Shop <shop@example.com>")->to("ann@example.org", "Bob Ünal <bob@example.org>")
            ->cc("\"Smith, Ann\" <ann.smith@example.org>")->bcc("archive@example.com")->replyto("help@example.com")
            ->subject("Your order 1042 — thank you, très bien, this subject is long enough to need several encoded words")
            ->text("Thank you.\n.a line with a dot\r\nbare cr\rend = sign, tab\tand trailing space \n" + long + "\nünïcödé\n")
            ->header("X-Mailer", "kukel")->header("List-Unsubscribe", "<mailto:unsub@example.com>"),
        new mail::Message()->from("a@example.com")->to("b@example.com")->subject(trim(long))->html("<p>Hi</p>"),
        new mail::Message()->from("a@example.com")->to("b@example.com")->text("plain")->html("<p>html</p>"),
        new mail::Message()->from("a@example.com")->to("b@example.com")->text("t")->html("<img src=\"cid:logo.png\">")
            ->embed("logo.png", "\x89PNG\0\x01\x02")->attach("Rechnung März.pdf", "%PDF-1.4 " + long)
            ->attach("data", "\0\x01\x02\x03", "application/x-thing"),
        new mail::Message()->from("a@example.com")->to("b@example.com"),
        new mail::Message()->from("a@example.com")->to("b@example.com")->text("t")->embed("x.gif", "GIF89a"),
    ];

    for (local m in ms) {
        m->date(new time::Time(2026, 11, 17, 10, 30, 0, "Europe/Amsterdam"));
        print(m->bytes() + "\0");
    }
}
`)
	msgs := strings.Split(strings.TrimSuffix(out, "\x00"), "\x00")
	if len(msgs) != 6 {
		t.Fatalf("%d messages", len(msgs))
	}
	long := ""
	for i := range 30 {
		long += "word" + itoa(i) + " "
	}

	m, parts, shape := parse(t, msgs[0])
	dec := new(mime.WordDecoder)
	subject, err := dec.DecodeHeader(m.Header.Get("Subject"))
	if err != nil || subject != "Your order 1042 — thank you, très bien, this subject is long enough to need several encoded words" {
		t.Errorf("subject %q, %v", subject, err)
	}
	to, err := m.Header.AddressList("To")
	if err != nil || len(to) != 2 || to[1].Name != "Bob Ünal" || to[1].Address != "bob@example.org" || to[0].Address != "ann@example.org" {
		t.Errorf("to %v, %v", to, err)
	}
	cc, err := m.Header.AddressList("Cc")
	if err != nil || len(cc) != 1 || cc[0].Name != "Smith, Ann" {
		t.Errorf("cc %v, %v", cc, err)
	}
	if m.Header.Get("Bcc") != "" || strings.Contains(msgs[0], "archive@") {
		t.Errorf("bcc shows in the message")
	}
	if d, err := m.Header.Date(); err != nil || !d.Equal(time.Date(2026, 11, 17, 9, 30, 0, 0, time.UTC)) || m.Header.Get("Date") != "Tue, 17 Nov 2026 10:30:00 +0100" {
		t.Errorf("date %q %v", m.Header.Get("Date"), err)
	}
	if id := m.Header.Get("Message-Id"); !strings.HasPrefix(id, "<") || !strings.HasSuffix(id, "@example.com>") {
		t.Errorf("Message-ID %q", id)
	}
	if m.Header.Get("X-Mailer") != "kukel" || m.Header.Get("List-Unsubscribe") != "<mailto:unsub@example.com>" || m.Header.Get("Reply-To") != "help@example.com" || m.Header.Get("Mime-Version") != "1.0" {
		t.Errorf("headers %v", m.Header)
	}
	want := "Thank you.\r\n.a line with a dot\r\nbare cr\r\nend = sign, tab\tand trailing space \r\n" + long + "\r\nünïcödé\r\n"
	if shape != "text/plain" || parts[0].body != want {
		t.Errorf("%s %q, want %q", shape, parts[0].body, want)
	}

	m, parts, shape = parse(t, msgs[1])
	if s, _ := dec.DecodeHeader(m.Header.Get("Subject")); s != strings.TrimSpace(long) || shape != "text/html" || parts[0].body != "<p>Hi</p>\r\n" {
		t.Errorf("%q %s %q", s, shape, parts[0].body)
	}

	_, parts, shape = parse(t, msgs[2])
	if shape != "alternative(text/plain text/html)" || parts[0].body != "plain\r\n" || parts[1].body != "<p>html</p>\r\n" {
		t.Errorf("%s %v", shape, parts)
	}

	_, parts, shape = parse(t, msgs[3])
	if shape != "mixed(alternative(text/plain related(text/html image/png)) application/pdf application/x-thing)" {
		t.Errorf("shape %s", shape)
	}
	if p := parts[2]; p.cid != "<logo.png>" || p.disposition != "inline" || p.body != "\x89PNG\x00\x01\x02" || p.filename != "logo.png" {
		t.Errorf("inline part %+v", p)
	}
	if p := parts[3]; p.filename != "Rechnung März.pdf" || p.disposition != "attachment" || p.body != "%PDF-1.4 "+long {
		t.Errorf("attachment %+v", p)
	}
	if p := parts[4]; p.filename != "data" || p.body != "\x00\x01\x02\x03" {
		t.Errorf("attachment %+v", p)
	}

	_, parts, shape = parse(t, msgs[4])
	if shape != "text/plain" || parts[0].body != "\r\n" {
		t.Errorf("an empty message: %s %q", shape, parts[0].body)
	}

	// An inline part without HTML to show it is an attachment beside the text.
	_, _, shape = parse(t, msgs[5])
	if shape != "mixed(text/plain image/gif)" {
		t.Errorf("shape %s", shape)
	}
}

func itoa(i int) string { return strconv.Itoa(i) }

// What a message refuses when it is set: line breaks in headers (header
// injection), malformed addresses, the message's own headers.
func TestMailRefusals(t *testing.T) {
	out := run(t, `
use mail;

try1(f) {
    try {
        f();
        println("no error");
    } catch (e) {
        println(e->kind + ": " + e->message);
    }
}

main() {
    local m = new mail::Message();
    try1(fn() { m->subject("hi\r\nBcc: victim@example.org"); });
    try1(fn() { m->to("ann@example.org\nBcc: victim@example.org"); });
    try1(fn() { m->from("Ann\r <ann@example.org>"); });
    try1(fn() { m->header("X-Note", "a\nb"); });
    try1(fn() { m->header("Bcc", "x@example.org"); });
    try1(fn() { m->header("Bad Name", "x"); });
    try1(fn() { m->to("no-at-sign"); });
    try1(fn() { m->to("a@b@c"); });
    try1(fn() { m->to("Ann <ann@example.org"); });
    try1(fn() { m->to("a b@example.org"); });
    try1(fn() { m->subject(5); });
    try1(fn() { m->attach("x", "data", "nonsense"); });
    try1(fn() { m->embed("a b", "data"); });
    try1(fn() { m->messageid("no-at"); });
    try1(fn() { m->bytes(); });
    try1(fn() { mail::envelope(m->from("a@example.com")); });
    println(m->to(), m->subject(), m->headers());
    println(mail::address("\"Smith, Ann\" <ann@example.org>"), mail::address("ann@example.org"));
    println(new mail::Message()->to("\"Smith, Ann\" <a@x.org>", "Ann <b@x.org>")->to());
}
`)
	want := `ValueError: mail: a line break in the subject: "hi\r\nBcc: victim@example.org" (it would add headers)
ValueError: mail: a line break in an address: "ann@example.org\nBcc: victim@example.org" (it would add headers)
ValueError: mail: a line break in an address: "Ann\r <ann@example.org>" (it would add headers)
ValueError: mail: a line break in the header X-Note: "a\nb" (it would add headers)
ValueError: mail: the header Bcc is the message's own; use its function
ValueError: mail: not a header name: "Bad Name"
ValueError: mail: not an address: "no-at-sign" (it needs one @ between a name and a domain)
ValueError: mail: not an address: "a@b@c" (it needs one @ between a name and a domain)
ValueError: mail: not an address: "Ann <ann@example.org"
ValueError: mail: not an address: "a b@example.org"
TypeError: mail::Message::subject: a string, not int
ValueError: mail: not a content type: "nonsense" (it is type/subtype)
ValueError: mail::Message::embed: the name is its Content-ID, so printable ASCII without spaces, < > or ": "a b"
ValueError: mail: not a Message-ID: "no-at" (it is <left@right>)
ValueError: mail: the message has no from address
ValueError: mail: the message has no recipients (to, cc or bcc)
[] nil []
{"name": "Smith, Ann", "address": "ann@example.org"} {"name": "", "address": "ann@example.org"}
["\"Smith, Ann\" <a@x.org>", "Ann <b@x.org>"]
`
	if out != want {
		t.Errorf("got\n%s\nwant\n%s", out, want)
	}
}

// pki: a CA and a certificate for localhost and 127.0.0.1, as PEM files.
type pki struct {
	ca   string
	pair tls.Certificate
}

func newPKI(t *testing.T) *pki {
	t.Helper()
	dir := t.TempDir()
	write := func(name, kind string, der []byte) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: kind, Bytes: der}), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	caTmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "Kukel Mail Test CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caCert, _ := x509.ParseCertificate(caDER)
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "localhost"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.IPv4(127, 0, 0, 1)},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	kder, _ := x509.MarshalPKCS8PrivateKey(key)
	p := &pki{ca: write("ca.pem", "CERTIFICATE", caDER)}
	p.pair, err = tls.X509KeyPair(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: kder}))
	if err != nil {
		t.Fatal(err)
	}
	return p
}

// offer is what an smtpd offers and refuses.
type offer struct {
	implicit bool     // TLS from the start (smtps)
	starttls bool     // offers STARTTLS
	auth     []string // the AUTH mechanisms it offers
	smtputf8 bool
	refuse   string // MAIL FROM or RCPT TO of an address with this in it: 550
}

// smtpd is a small SMTP server for the tests: what it offers is set by
// its fields; it logs the commands (AUTH as "AUTH <mechanism> <user>")
// and keeps the data of each message, with the dots taken out again.
type smtpd struct {
	t   *testing.T
	pki *pki
	offer

	mu    sync.Mutex
	log   []string
	data  []string // as received, dots doubled
	clean []string // with the dots taken out
	addr  string
}

func (s *smtpd) start() string {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		s.t.Fatal(err)
	}
	s.t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go s.serve(c)
		}
	}()
	s.addr = ln.Addr().String()
	return s.addr
}

func (s *smtpd) note(l string) {
	s.mu.Lock()
	s.log = append(s.log, l)
	s.mu.Unlock()
}

func (s *smtpd) serve(c net.Conn) {
	defer c.Close()
	conf := &tls.Config{Certificates: []tls.Certificate{s.pki.pair}}
	if s.implicit {
		c = tls.Server(c, conf)
	}
	r, secure := bufio.NewReader(c), s.implicit
	say := func(l string) { io.WriteString(c, l+"\r\n") }
	say("220 test ESMTP")
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		verb := strings.ToUpper(strings.SplitN(line, " ", 2)[0])
		switch {
		case verb == "EHLO":
			s.note("EHLO")
			say("250-test")
			if s.starttls && !secure {
				say("250-STARTTLS")
			}
			if len(s.auth) > 0 {
				say("250-AUTH " + strings.Join(s.auth, " "))
			}
			if s.smtputf8 {
				say("250-SMTPUTF8")
			}
			say("250 8BITMIME")
		case verb == "STARTTLS":
			s.note("STARTTLS")
			say("220 go ahead")
			tc := tls.Server(c, conf)
			if err := tc.Handshake(); err != nil {
				return
			}
			c, r, secure = tc, bufio.NewReader(tc), true
		case verb == "AUTH":
			f := strings.Fields(line)
			var user, pass string
			if strings.ToUpper(f[1]) == "PLAIN" {
				b, _ := base64.StdEncoding.DecodeString(f[2])
				p := strings.Split(string(b), "\x00")
				user, pass = p[1], p[2]
			} else {
				say("334 VXNlcm5hbWU6")
				l, _ := r.ReadString('\n')
				b, _ := base64.StdEncoding.DecodeString(strings.TrimSpace(l))
				user = string(b)
				say("334 UGFzc3dvcmQ6")
				l, _ = r.ReadString('\n')
				b, _ = base64.StdEncoding.DecodeString(strings.TrimSpace(l))
				pass = string(b)
			}
			s.note("AUTH " + strings.ToUpper(f[1]) + " " + user)
			if pass == "s3cr@t" {
				say("235 2.7.0 ok")
			} else {
				say("535 5.7.8 bad credentials")
			}
		case strings.HasPrefix(line, "MAIL FROM:"), strings.HasPrefix(line, "RCPT TO:"):
			s.note(line)
			if s.refuse != "" && strings.Contains(line, s.refuse) {
				say("550 5.1.1 " + line[strings.Index(line, ":")+1:] + ": no such user")
			} else {
				say("250 ok")
			}
		case verb == "DATA":
			s.note("DATA")
			say("354 go on")
			var raw, clean strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" {
					break
				}
				raw.WriteString(l)
				clean.WriteString(strings.TrimPrefix(l, "."))
			}
			s.mu.Lock()
			s.data = append(s.data, raw.String())
			s.clean = append(s.clean, clean.String())
			s.mu.Unlock()
			say("250 2.0.0 queued as 42")
		case verb == "RSET":
			s.note("RSET")
			say("250 ok")
		case verb == "QUIT":
			s.note("QUIT")
			say("221 bye")
			return
		default:
			say("500 what")
		}
	}
}

// fill puts the server's address and the CA file into a program: ADDR,
// PORT, CA (quoted).
func fill(src, addr string, p *pki) string {
	_, port, _ := net.SplitHostPort(addr)
	return strings.NewReplacer("ADDR", addr, "PORT", port, "CA", `"`+strings.ReplaceAll(p.ca, `\`, `\\`)+`"`).Replace(src)
}

// The SMTP conversation: STARTTLS, AUTH PLAIN, two messages over one
// connection (RSET between them), dot-stuffing, the reply; the data the
// server got is the message, and its bare line endings and dots cannot
// end it early.
func TestMailSMTP(t *testing.T) {
	p := newPKI(t)
	s := &smtpd{t: t, pki: p, offer: offer{starttls: true, auth: []string{"LOGIN", "PLAIN"}}}
	addr := s.start()
	out := run(t, fill(`
use mail;

main() {
    local t = mail::transport({"scheme": "smtp", "host": "localhost", "port": PORT, "user": "shop", "password": "s3cr@t", "ca": CA, "helo": "client.example"});
    println(t);
    local m = new mail::Message()->from("shop@example.com")->to("ann@example.org")->cc("bob@example.org")->bcc("arch@example.com")
        ->subject("hi")->text("one\n.\n..two\r\n.\rthree\n");
    local r = t->send(m);
    println(r["reply"], r["envelope"], r["messageid"] == m->messageid());
    print(m->bytes() + "\0");
    t->send(m, {"from": "bounce@example.com", "to": ["x@example.org"]});
    t->close();
    t->close();
}
`, addr, p))
	lines := strings.SplitN(out, "\x00", 2)
	head := strings.SplitN(lines[0], "\n", 3)
	if head[0] != `mail::SmtpTransport{dsn: "smtp://shop:***@localhost:`+portOf(addr)+`", `+
		`_c: {"scheme": "smtp", "host": "localhost", "port": `+portOf(addr)+`, "user": "shop", "ca": `+strconvQuote(p.ca)+`, "helo": "client.example", "timeout": 30}, `+
		`_password: <function mail::SmtpTransport::SmtpTransport/fn@`+fnLine(t)+`>, _conn: nil, _ext: nil}` {
		t.Errorf("transport prints as %s", head[0])
	}
	if strings.Contains(out, "s3cr@t") {
		t.Errorf("the password shows")
	}
	if head[1] != `250 2.0.0 queued as 42 {"from": "shop@example.com", "to": ["ann@example.org", "bob@example.org", "arch@example.com"]} true` {
		t.Errorf("send gave %s", head[1])
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	want := "EHLO STARTTLS EHLO|AUTH PLAIN shop|MAIL FROM:<shop@example.com>|RCPT TO:<ann@example.org>|RCPT TO:<bob@example.org>|RCPT TO:<arch@example.com>|DATA|" +
		"RSET|MAIL FROM:<bounce@example.com>|RCPT TO:<x@example.org>|DATA|QUIT"
	if got := strings.Replace(strings.Join(s.log, "|"), "EHLO|STARTTLS|EHLO", "EHLO STARTTLS EHLO", 1); got != want {
		t.Errorf("conversation\n%s\nwant\n%s", got, want)
	}
	if len(s.clean) != 2 || s.clean[0] != head[2] {
		t.Fatalf("the server got\n%q\nthe message is\n%q", s.clean, head[2])
	}
	if !strings.Contains(s.data[0], "\r\n..\r\n...two\r\n..\r\nthree\r\n") {
		t.Errorf("not dot-stuffed: %q", s.data[0])
	}
}

func portOf(addr string) string {
	_, port, _ := net.SplitHostPort(addr)
	return port
}

func strconvQuote(s string) string {
	return `"` + strings.ReplaceAll(s, `\`, `\\`) + `"`
}

// fnLine gives the line of the password's closure in mail.kkl.
func fnLine(t *testing.T) string {
	src, err := os.ReadFile("mail.kkl")
	if err != nil {
		t.Fatal(err)
	}
	for i, l := range strings.Split(string(src), "\n") {
		if strings.Contains(l, "_password = fn()") {
			return itoa(i + 1)
		}
	}
	t.Fatal("no _password = fn() in mail.kkl")
	return ""
}

// The cases that must fail, and those that must not.
func TestMailSMTPCases(t *testing.T) {
	p := newPKI(t)
	type tc struct {
		name   string
		server offer
		dsn    string // ADDR, PORT and CA filled in
		want   string // what the program prints: the reply, or the error
		log    string // the commands the server saw
	}
	cases := []tc{
		{"no STARTTLS offered", offer{}, `"smtp://[::ffff:127.0.0.1]:PORT"`,
			"MailError: mail: smtp://[::ffff:127.0.0.1]:PORT does not offer STARTTLS, so the mail would go in plain text; smtps:// or ?tls=0 to allow that",
			"EHLO|QUIT"},
		{"plain allowed by tls=0", offer{starttls: true}, `"smtp://[::ffff:127.0.0.1]:PORT?tls=0"`,
			"250 2.0.0 queued as 42", "EHLO|MAIL FROM:<a@example.com>|RCPT TO:<b@example.org>|DATA|QUIT"},
		{"localhost without STARTTLS", offer{}, `"smtp://127.0.0.1:PORT"`,
			"250 2.0.0 queued as 42", "EHLO|MAIL FROM:<a@example.com>|RCPT TO:<b@example.org>|DATA|QUIT"},
		{"certificate not trusted", offer{starttls: true}, `"smtp://localhost:PORT"`,
			"NetError: net::Conn::starttls: TLS handshake failed: certificate signed by unknown authority", "EHLO|STARTTLS"},
		{"verify=0", offer{starttls: true}, `"smtp://localhost:PORT?verify=0"`,
			"250 2.0.0 queued as 42", "EHLO|STARTTLS|EHLO|MAIL FROM:<a@example.com>|RCPT TO:<b@example.org>|DATA|QUIT"},
		{"smtps", offer{implicit: true, auth: []string{"PLAIN"}}, `{"scheme": "smtps", "host": "localhost", "port": PORT, "ca": CA, "user": "u", "password": "s3cr@t"}`,
			"250 2.0.0 queued as 42", "EHLO|AUTH PLAIN u|MAIL FROM:<a@example.com>|RCPT TO:<b@example.org>|DATA|QUIT"},
		{"AUTH LOGIN only", offer{starttls: true, auth: []string{"LOGIN"}}, `{"scheme": "smtp", "host": "localhost", "port": PORT, "ca": CA, "user": "u@x", "password": "s3cr@t"}`,
			"250 2.0.0 queued as 42", "EHLO|STARTTLS|EHLO|AUTH LOGIN u@x|MAIL FROM:<a@example.com>|RCPT TO:<b@example.org>|DATA|QUIT"},
		{"a wrong password", offer{starttls: true, auth: []string{"PLAIN"}}, `{"scheme": "smtp", "host": "localhost", "port": PORT, "ca": CA, "user": "u", "password": "wrong"}`,
			"MailError: mail: smtp://u:***@localhost:PORT: authentication failed: 535 5.7.8 bad credentials 535", "EHLO|STARTTLS|EHLO|AUTH PLAIN u|QUIT"},
		{"no AUTH offered", offer{starttls: true}, `{"scheme": "smtp", "host": "localhost", "port": PORT, "ca": CA, "user": "u", "password": "x"}`,
			"MailError: mail: smtp://u:***@localhost:PORT offers no AUTH, but the DSN has a user", "EHLO|STARTTLS|EHLO|QUIT"},
		{"a recipient refused", offer{refuse: "nosuch"}, `"smtp://127.0.0.1:PORT"`,
			"MailError: mail: the server refused 1 of 2 recipients, so the message was not sent: 550 5.1.1 <nosuch@example.org>: no such user [{\"address\": \"nosuch@example.org\", \"reply\": \"550 5.1.1 <nosuch@example.org>: no such user\"}]",
			"EHLO|MAIL FROM:<a@example.com>|RCPT TO:<b@example.org>|RCPT TO:<nosuch@example.org>|RSET|QUIT"},
		{"the sender refused", offer{refuse: "a@example.com"}, `"smtp://127.0.0.1:PORT"`,
			"MailError: mail: smtp://127.0.0.1:PORT: MAIL FROM: 550 5.1.1 <a@example.com>: no such user 550", "EHLO|MAIL FROM:<a@example.com>|QUIT"},
		{"SMTPUTF8 needed, not offered", offer{}, `"smtp://127.0.0.1:PORT"`,
			"MailError: mail: the server does not take SMTPUTF8, which an address that is not ASCII needs", "EHLO|QUIT"},
		{"SMTPUTF8 offered", offer{smtputf8: true}, `"smtp://127.0.0.1:PORT"`,
			"250 2.0.0 queued as 42", "EHLO|MAIL FROM:<a@example.com> SMTPUTF8|RCPT TO:<b@example.org>|RCPT TO:<jürgen@example.org>|DATA|QUIT"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := &smtpd{t: t, pki: p, offer: c.server}
			addr := s.start()
			to := `"b@example.org"`
			switch c.name {
			case "a recipient refused":
				to = `"b@example.org", "nosuch@example.org"`
			case "SMTPUTF8 needed, not offered", "SMTPUTF8 offered":
				to = `"b@example.org", "jürgen@example.org"`
			}
			out := run(t, fill(`
use mail;

main() {
    local t = mail::transport(`+c.dsn+`);

    try {
        println(t->send(new mail::Message()->from("a@example.com")->to(`+to+`)->text("x"))["reply"]);
    } catch (e) {
        println(e->kind + ": " + e->message, e->kind == "MailError" ? (e->code != nil ? e->code : (len(e->refused) > 0 ? e->refused : "")) : "");
    }
    t->close();
}
`, addr, p))
			want := strings.ReplaceAll(c.want, "PORT", portOf(addr))
			if strings.TrimRight(out, " \n") != want {
				t.Errorf("got  %s\nwant %s", strings.TrimRight(out, " \n"), want)
			}
			// The server notes QUIT after the program ended; wait for it.
			for range 100 {
				s.mu.Lock()
				n := len(s.log)
				done := n > 0 && (s.log[n-1] == "QUIT" || !strings.HasSuffix(c.log, "QUIT"))
				s.mu.Unlock()
				if done {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			s.mu.Lock()
			got := strings.Join(s.log, "|")
			s.mu.Unlock()
			if got != c.log {
				t.Errorf("conversation %s, want %s", got, c.log)
			}
		})
	}
}

// DSNs: what transport() takes and refuses.
func TestMailDSN(t *testing.T) {
	out := run(t, `
use mail;

main() {
    for (local d in [
        "smtp://mail.example.com", "smtps://u:p%40ss@mail.example.com", "smtp://[::1]:2525?timeout=5&helo=me.example",
        "memory://", "null://", "smtp://", "ftp://x", "smtp://h:99999", "smtp://h?bogus=1", "smtp://h?tls=yes",
        "smtps://h?tls=0", "memory://host", "not a dsn", "smtp://:pw@h", 42, {"scheme": "smtp", "host": "h", "port": "25"},
        {"scheme": "smtp", "host": "h", "nope": 1}, {"scheme": "smtp", "host": "h", "user": "u\r\nx"},
    ]) {
        try {
            local t = mail::transport(d);
            println(instanceof(t, mail::SmtpTransport) ? t->dsn : t);
        } catch (e) {
            println(e->kind + ": " + e->message);
        }
    }
    local t = mail::transport("memory://"), m = new mail::Message()->from("a@example.com")->to("b@example.org")->text("x");
    t->send(m);
    println(len(t->sent()), t->sent()[0]["envelope"], find(t->sent()[0]["bytes"], "\r\n\r\nx\r\n") > 0);
    println(mail::transport("null://")->send(m)["reply"]);
}
`)
	want := `smtp://mail.example.com:25
smtps://u:***@mail.example.com:465
smtp://[::1]:2525
mail::MemoryTransport{_sent: []}
mail::NullTransport{}
ValueError: mail::transport: smtp:// needs a host
ValueError: mail::transport: an unknown scheme "ftp" (smtp, smtps, memory, null)
ValueError: mail::transport: the port 99999 is not 1 to 65535
ValueError: mail::transport: an unknown option "bogus" (tls, verify, ca, timeout, helo)
ValueError: mail::transport: tls is 0 or 1, not "yes"
ValueError: mail::transport: smtps:// is TLS; tls=0 is for smtp://
ValueError: mail::transport: memory:// takes no host
ValueError: mail::transport: not a DSN: "not a dsn" (scheme://user:password@host:port?options)
ValueError: mail::transport: a password without a user
TypeError: mail::transport: a DSN string or map, not int
TypeError: mail::transport: port is an int, not string
ValueError: mail::transport: an unknown key "nope"
ValueError: mail: a line break in the user: "u\r\nx" (it would add headers)
1 {"from": "a@example.com", "to": ["b@example.org"]} true
250 dropped
`
	if out != want {
		t.Errorf("got\n%s\nwant\n%s", out, want)
	}
}
