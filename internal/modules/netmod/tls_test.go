package netmod_test

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// pki is a test CA and a certificate it signed for localhost and
// 127.0.0.1, as PEM files in a directory of the test.
type pki struct {
	dir                     string
	ca, cert, key           string // the files
	caPool                  *x509.CertPool
	pair                    tls.Certificate
	clientCert, clientKey   string // a client certificate of the same CA
	expiredCert, expiredKey string
}

func newPKI(t *testing.T) *pki {
	t.Helper()
	p := &pki{dir: t.TempDir()}
	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	caTmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Kukel Test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTmpl, caTmpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caCert, _ := x509.ParseCertificate(caDER)
	p.caPool = x509.NewCertPool()
	p.caPool.AddCert(caCert)
	p.ca = p.write(t, "ca.pem", "CERTIFICATE", caDER)

	leaf := func(name string, serial int64, client bool, notAfter time.Time) (certFile, keyFile string) {
		key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		tmpl := &x509.Certificate{
			SerialNumber: big.NewInt(serial),
			Subject:      pkix.Name{CommonName: name},
			NotBefore:    time.Now().Add(-2 * time.Hour),
			NotAfter:     notAfter,
			KeyUsage:     x509.KeyUsageDigitalSignature,
			ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
			DNSNames:     []string{"localhost"},
			IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1)},
		}
		if client {
			tmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
			tmpl.DNSNames, tmpl.IPAddresses = nil, nil
		}
		der, err := x509.CreateCertificate(rand.Reader, tmpl, caCert, &key.PublicKey, caKey)
		if err != nil {
			t.Fatal(err)
		}
		kder, _ := x509.MarshalPKCS8PrivateKey(key)
		return p.write(t, name+".pem", "CERTIFICATE", der), p.write(t, name+".key", "PRIVATE KEY", kder)
	}
	p.cert, p.key = leaf("localhost", 2, false, time.Now().Add(24*time.Hour))
	p.clientCert, p.clientKey = leaf("client", 3, true, time.Now().Add(24*time.Hour))
	p.expiredCert, p.expiredKey = leaf("expired", 4, false, time.Now().Add(-time.Hour))
	p.pair, err = tls.LoadX509KeyPair(p.cert, p.key)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func (p *pki) write(t *testing.T, name, kind string, der []byte) string {
	t.Helper()
	path := filepath.Join(p.dir, name)
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: kind, Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// fill puts the files of p into src: CA, CERT, KEY, CLIENTCERT, CLIENTKEY,
// EXPIRED, EXPIREDKEY (quoted, as Kukel strings).
func (p *pki) fill(src string) string {
	q := func(s string) string { return `"` + strings.ReplaceAll(s, `\`, `\\`) + `"` }
	return strings.NewReplacer(
		"CLIENTCERT", q(p.clientCert), "CLIENTKEY", q(p.clientKey),
		"EXPIREDKEY", q(p.expiredKey), "EXPIRED", q(p.expiredCert),
		"CA", q(p.ca), "CERT", q(p.cert), "KEY", q(p.key),
	).Replace(src)
}

// A Kukel program talks to its own TLS listener: the handshake, data both
// ways, what c->tls() tells, how the connection prints.
func TestTLS(t *testing.T) {
	p := newPKI(t)
	got := run(t, p.fill(`use net;
main() {
    local l = net::listen("tls://127.0.0.1:0", {"cert": CERT, "key": KEY});
    println(find(l->endpoint(), "tls://127.0.0.1:") == 0, str(l) == "net::Listener(" + l->endpoint() + ")");
    local c = net::connect(l->endpoint(), 5, {"ca": CA}), s = l->accept(5);
    c->println("hello");
    println(s->readline());
    s->write("world\n");
    println(c->readline());
    local info = c->tls();
    println(info["version"], info["servername"], info["subject"], info["issuer"], info["verified"]);
    println(typeof(info["expires"]), typeof(info["cipher"]), len(info["cipher"]) > 0);
    println(s->tls()["servername"] == "", haskey(s->tls(), "subject"));  // no name is sent for an IP address
    println(str(c) == "net::Conn(" + c->peer() + ", TLS)", find(c->peer(), "tcp://") == 0);
    c->closewrite();
    println(s->readall() == "");
    c->close(); s->close();

    local port = split(l->endpoint(), ":")[2];
    c = net::connect("tls://localhost:" + port, 5, {"ca": CA}); s = l->accept(5);
    println(c->tls()["servername"], s->tls()["servername"]);
    c->close(); s->close();
    c = net::connect(l->endpoint(), nil, {"insecure": true}); s = l->accept(5);
    println(c->tls()["verified"]);
    c->close(); s->close();
    c = net::connect("tcp://" + split(l->endpoint(), "//")[1]);
    println(c->tls());
    c->close(); l->close();
}`))
	want := "true true\n" +
		"hello\nworld\n" +
		"TLS 1.3 127.0.0.1 CN=localhost CN=Kukel Test CA true\n" +
		"object string true\n" +
		"true false\n" +
		"true true\n" +
		"true\n" +
		"localhost localhost\n" +
		"false\n" +
		"nil\n"
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

// What is refused: an unknown authority, the wrong name, an expired
// certificate, options that are wrong, and a listener without its
// certificate.
func TestTLSErrors(t *testing.T) {
	p := newPKI(t)
	got := run(t, p.fill(`use net;
main() {
    local l = net::listen("tls://127.0.0.1:0", {"cert": CERT, "key": KEY});
    local e = net::listen("tls://127.0.0.1:0", {"cert": EXPIRED, "key": EXPIREDKEY});
    local tries = [
        fn() { net::connect(l->endpoint(), 5); },
        fn() { net::connect(l->endpoint(), 5, {"ca": CA, "servername": "example.com"}); },
        fn() { net::connect(e->endpoint(), 5, {"ca": CA}); },
        fn() { net::connect(l->endpoint(), 5, {"ca": CA, "colour": "red"}); },
        fn() { net::connect(l->endpoint(), 5, {"ca": 1}); },
        fn() { net::connect(l->endpoint(), 5, {"insecure": "yes"}); },
        fn() { net::connect(l->endpoint(), 5, {"cert": CERT}); },
        fn() { net::connect(l->endpoint(), 5, {"ca": "no-such.pem"}); },
        fn() { net::connect(l->endpoint(), 5, {"ca": KEY}); },
        fn() { net::connect(l->endpoint(), 5, []); },
        fn() { net::connect("tcp://127.0.0.1:1", 5, {"ca": CA}); },
        fn() { net::listen("tls://127.0.0.1:0"); },
        fn() { net::listen("tls://127.0.0.1:0", {"ca": CA}); },
        fn() { net::listen("tls://127.0.0.1:0", {"cert": CA, "key": KEY}); },
        fn() { net::listen("tcp://127.0.0.1:0", {"cert": CERT, "key": KEY}); },
    ];
    for (local f in tries)
        try { f(); println("no error"); } catch (x) { println(x->kind, x->message); }
    l->close(); e->close();
}`))
	got = strings.ReplaceAll(got, p.dir+string(os.PathSeparator), "DIR/")
	for _, line := range []string{
		": TLS handshake failed: certificate signed by unknown authority\n",
		": TLS handshake failed: certificate is valid for localhost, not example.com\n",
		": TLS handshake failed: certificate has expired or is not yet valid: current time",
		`ValueError net::connect: unknown option "colour" (it takes ca, cert, key, servername, insecure)`,
		"TypeError net::connect: option ca must be a string that is not empty, not int",
		"TypeError net::connect: option insecure must be true or false, not string",
		"ValueError net::connect: options cert and key go together",
		"IOError net::connect: no-such.pem: no such file or directory",
		"ValueError net::connect: DIR/localhost.key: no certificates in it (PEM)",
		"TypeError net::connect: argument 3 must be a map of options, not vector",
		`ValueError net::connect: options are for "tls://" addresses`,
		"ValueError net::listen: a TLS listener needs the options cert and key",
		`ValueError net::listen: unknown option "ca" (it takes cert, key)`,
		"ValueError net::listen: DIR/ca.pem and DIR/localhost.key: private key does not match public key",
		`ValueError net::listen: options are for "tls://" addresses`,
	} {
		if !strings.Contains(got, line+"\n") && !strings.Contains(got, line) {
			t.Errorf("missing %q", line)
		}
	}
	if t.Failed() {
		t.Logf("got\n%s", got)
	}
}

// server is a plain TCP server in Go for the tests: serve gets each
// connection (and closes it after).
func server(t *testing.T, serve func(net.Conn)) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() { defer c.Close(); serve(c) }()
		}
	}()
	return "tcp://" + ln.Addr().String()
}

// smtpish speaks the start of SMTP's STARTTLS, then echoes a line
// encrypted. With inject, it sends a line of its own right after its
// "220 go ahead", in the same write: what someone in between would add.
func smtpish(p *pki, inject bool, clientCAs *x509.CertPool) func(net.Conn) {
	return func(c net.Conn) {
		r := bufio.NewReader(c)
		c.Write([]byte("220 ready\r\n"))
		if line, _ := r.ReadString('\n'); line != "STARTTLS\r\n" {
			return
		}
		if inject {
			c.Write([]byte("220 go ahead\r\n250 INJECTED\r\n"))
		} else {
			c.Write([]byte("220 go ahead\r\n"))
		}
		cfg := &tls.Config{Certificates: []tls.Certificate{p.pair}}
		if clientCAs != nil {
			cfg.ClientCAs, cfg.ClientAuth = clientCAs, tls.RequireAndVerifyClientCert
		}
		tc := tls.Server(c, cfg)
		if tc.Handshake() != nil {
			return
		}
		tr := bufio.NewReader(tc)
		line, _ := tr.ReadString('\n')
		who := "nobody"
		if st := tc.ConnectionState(); len(st.PeerCertificates) > 0 {
			who = st.PeerCertificates[0].Subject.CommonName
		}
		tc.Write([]byte("echo " + strings.TrimSpace(line) + " from " + who + "\r\n"))
	}
}

// STARTTLS: plain first, then encrypted on the same connection; with a
// client certificate when the server asks for one.
func TestStartTLS(t *testing.T) {
	p := newPKI(t)
	plain := server(t, smtpish(p, false, nil))
	withClient := server(t, smtpish(p, false, p.caPool))
	got := run(t, p.fill(`use net;
main(args) {
    local addr = args[0], addr2 = args[1];
    local c = net::connect(addr, 5)->timeout(5);
    println(c->readline());
    c->write("STARTTLS\r\n");
    println(c->readline(), c->tls());
    println(c->starttls("localhost", {"ca": CA}) == c, c->tls()["servername"], c->tls()["verified"]);
    c->write("hi\r\n");
    println(c->readline());
    try { c->starttls("localhost"); } catch (e) { println(e->kind, e->message); }
    c->close();

    c = net::connect(addr2, 5)->timeout(5);
    c->readline(); c->write("STARTTLS\r\n"); c->readline();
    c->starttls("127.0.0.1", {"ca": CA, "cert": CLIENTCERT, "key": CLIENTKEY});
    c->write("hi\r\n");
    println(c->readline());
    c->close();
}`), plain, withClient)
	want := "220 ready\n" +
		"220 go ahead nil\n" +
		"true localhost true\n" +
		"echo hi from nobody\n" +
		"ValueError net::Conn::starttls: the connection is encrypted already\n" +
		"echo hi from client\n"
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

// Data that came before the handshake is refused: the STARTTLS command
// injection (CVE-2011-0411 and its kind). Also: a server that does not
// speak TLS, a handshake that does not come in time, the wrong name.
func TestStartTLSRefused(t *testing.T) {
	p := newPKI(t)
	injecting := server(t, smtpish(p, true, nil))
	plain := server(t, func(c net.Conn) {
		c.Write([]byte("220 ready\r\n"))
		bufio.NewReader(c).ReadString('\n')
		c.Write([]byte("this is not TLS at all, but plain text of some length\r\n"))
		time.Sleep(time.Second)
	})
	silent := server(t, func(c net.Conn) {
		c.Write([]byte("220 ready\r\n"))
		time.Sleep(2 * time.Second)
	})
	honest := server(t, smtpish(p, false, nil))
	got := run(t, p.fill(`use net; use time;
main(args) {
    local inj = args[0], plain = args[1], silent = args[2], honest = args[3];
    local c = net::connect(inj, 5)->timeout(5);
    c->readline(); c->write("STARTTLS\r\n");
    time::sleep(0.2);                       // both lines have come
    println(c->readline());
    try { c->starttls("localhost", {"ca": CA}); } catch (e) { println(e->kind, e->message); }
    println(c->closed());
    c->close();

    c = net::connect(plain, 5)->timeout(5);
    c->readline(); c->write("STARTTLS\r\n");
    try { c->starttls("localhost", {"ca": CA}); } catch (e) { println(e->kind, e->message); }
    println(c->closed());

    c = net::connect(silent, 5)->timeout(0.2);
    c->readline();
    try { c->starttls("localhost", {"ca": CA}); } catch (e) { println(e->kind, e->message); }

    c = net::connect(honest, 5)->timeout(5);
    c->readline(); c->write("STARTTLS\r\n"); c->readline();
    try { c->starttls("example.com", {"ca": CA}); } catch (e) { println(e->kind, e->message); }
    try { c->starttls(""); } catch (e) { println(e->kind, e->message); }
}`), injecting, plain, silent, honest)
	want := "220 go ahead\n" +
		"NetError net::Conn::starttls: 14 bytes came before the TLS handshake and were not read; refused, as someone in between could have sent them (the connection is closed)\n" +
		"true\n" +
		"NetError net::Conn::starttls: TLS handshake failed: the other side does not speak TLS\n" +
		"true\n" +
		"NetError net::Conn::starttls: no TLS handshake within 0.2 seconds (the connection is closed)\n" +
		"NetError net::Conn::starttls: TLS handshake failed: certificate is valid for localhost, not example.com\n" +
		"ValueError net::Conn::starttls: the connection is closed\n"
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

// A TLS echo server on the event loop: onconnect and ondata work on
// encrypted connections as on plain ones.
func TestTLSEvents(t *testing.T) {
	p := newPKI(t)
	got := run(t, p.fill(`use net; use events; use worker;
main() {
    local srv = net::listen("tls://127.0.0.1:0", {"cert": CERT, "key": KEY});
    srv->onconnect(fn(conn) {
        conn->ondata(fn(d) {
            if (d == nil) { conn->close(); srv->close(); return; }
            conn->write(upper(d));
        });
    });
    local c = net::connect(srv->endpoint(), 5, {"ca": CA});
    c->ondata(fn(d) {
        if (d != nil) { println(d); c->close(); }
    });
    c->write("shout");
    c->closewrite();
    events::run();
    println("done");
}`))
	if got != "SHOUT\ndone\n" {
		t.Errorf("got %q", got)
	}
}
