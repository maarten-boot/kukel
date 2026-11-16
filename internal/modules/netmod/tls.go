package netmod

// TLS (TODO.md Q113, docs/future.md §11.2): connections encrypted from
// the start ("tls://host:port"), STARTTLS on an open connection
// (c->starttls), and listeners that hand out encrypted connections.

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"os"
	"strings"
	"time"

	"github.com/maarten-boot/kukel/internal/modules/timemod"
	"github.com/maarten-boot/kukel/internal/native"
	"github.com/maarten-boot/kukel/internal/value"
)

// handshakeTimeout bounds the handshake a listener does for a connection
// that came: a client that connects and says nothing is dropped then.
const handshakeTimeout = 10 * time.Second

// tlsOptions are the options of a TLS connection or listener (a map in
// Kukel): "ca" (a file of root certificates, PEM), "cert" and "key" (a
// certificate and its key, PEM files: the client's, or the listener's),
// "servername" (the name the server's certificate must hold, for
// net::connect) and "insecure" (true: the certificate is not checked).
type tlsOptions struct {
	ca, cert, key, servername string
	insecure                  bool
}

// readTLSOptions reads the map of argument i; allowed are the option
// names this call takes.
func readTLSOptions(a native.Args, i int, allowed ...string) (tlsOptions, error) {
	var o tlsOptions
	if a.V[i].Kind() != value.Map {
		return o, a.TypeError(i, "a map of options")
	}
	var bad error
	a.V[i].AsMap().Each(func(k, v value.Value) {
		if bad != nil {
			return
		}
		if k.Kind() != value.String {
			bad = a.Errorf(value.ValueError, "an option is named by a string, not %s", native.TypeName(k))
			return
		}
		key := k.AsString()
		known := false
		for _, name := range allowed {
			known = known || name == key
		}
		if !known {
			bad = a.Errorf(value.ValueError, "unknown option %q (it takes %s)", key, strings.Join(allowed, ", "))
			return
		}
		if key == "insecure" {
			if v.Kind() != value.Bool {
				bad = a.Errorf(value.TypeError, "option insecure must be true or false, not %s", native.TypeName(v))
				return
			}
			o.insecure = v.AsBool()
			return
		}
		if v.Kind() != value.String || v.AsString() == "" {
			bad = a.Errorf(value.TypeError, "option %s must be a string that is not empty, not %s", key, native.TypeName(v))
			return
		}
		switch s := v.AsString(); key {
		case "ca":
			o.ca = s
		case "cert":
			o.cert = s
		case "key":
			o.key = s
		case "servername":
			o.servername = s
		}
	})
	if bad == nil && (o.cert == "") != (o.key == "") {
		bad = a.Errorf(value.ValueError, "options cert and key go together")
	}
	return o, bad
}

// config makes the Go configuration of a client (server false: the name
// is the server's that its certificate must hold) or a listener.
func (o tlsOptions) config(a native.Args, servername string, server bool) (*tls.Config, error) {
	cfg := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: servername, InsecureSkipVerify: o.insecure}
	if o.ca != "" {
		pem, err := os.ReadFile(o.ca)
		if err != nil {
			return nil, native.IOError(a, err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, a.Errorf(value.ValueError, "%s: no certificates in it (PEM)", o.ca)
		}
		cfg.RootCAs = pool
	}
	if o.cert != "" {
		pair, err := tls.LoadX509KeyPair(o.cert, o.key)
		if err != nil {
			var pe *os.PathError
			if errors.As(err, &pe) {
				return nil, native.IOError(a, err)
			}
			return nil, a.Errorf(value.ValueError, "%s and %s: %s", o.cert, o.key, strings.TrimPrefix(err.Error(), "tls: "))
		}
		cfg.Certificates = []tls.Certificate{pair}
	}
	if server && o.cert == "" {
		return nil, a.Errorf(value.ValueError, "a TLS listener needs the options cert and key")
	}
	return cfg, nil
}

// tlsMessage words what failed in a handshake, without Go's prefixes
// ("certificate signed by unknown authority"); ok is false for errors
// that are not about TLS (the network's), which NetMessage words.
func tlsMessage(err error) (msg string, ok bool) {
	var cv *tls.CertificateVerificationError
	var rh tls.RecordHeaderError
	var op *net.OpError
	switch {
	case errors.As(err, &cv):
		return strings.TrimPrefix(cv.Err.Error(), "x509: "), true
	case errors.As(err, &rh):
		return "the other side does not speak TLS", true
	case errors.As(err, &op) && op.Op == "remote error":
		return "the other side refused: " + strings.TrimPrefix(op.Err.Error(), "tls: "), true
	}
	if s := err.Error(); strings.HasPrefix(s, "tls: ") {
		return strings.TrimPrefix(s, "tls: "), true
	}
	return "", false
}

// handshakeError is the NetError of a failed connect or starttls.
func handshakeError(a native.Args, prefix string, err error) error {
	if msg, ok := tlsMessage(err); ok {
		return a.Errorf(NetError, "%sTLS handshake failed: %s", prefix, msg)
	}
	return a.Errorf(NetError, "%s%s", prefix, native.NetMessage(err))
}

// c->starttls(servername [, options]): the TLS handshake on an open plain
// connection; from then on it is encrypted. Data that came but was not
// read yet is refused: it could only have been sent before the
// encryption, by someone in between, to be read as if it came after.
func starttls(a native.Args, env value.Env, c *conn) (value.Value, error) {
	if _, ok := c.c.(*tls.Conn); ok {
		return value.NilValue, a.Errorf(value.ValueError, "the connection is encrypted already")
	}
	servername, err := a.String(0)
	if err != nil {
		return value.NilValue, err
	}
	if servername == "" {
		return value.NilValue, a.Errorf(value.ValueError, "no server name")
	}
	var o tlsOptions
	if a.Has(1) {
		if o, err = readTLSOptions(a, 1, "ca", "cert", "key", "insecure"); err != nil {
			return value.NilValue, err
		}
	}
	cfg, err := o.config(a, servername, false)
	if err != nil {
		return value.NilValue, err
	}
	if len(c.buf) > 0 { // closed too, so that nothing goes on in plain text by mistake
		n := len(c.buf)
		c.shut()
		return value.NilValue, a.Errorf(NetError, "%d bytes came before the TLS handshake and were not read; refused, as someone in between could have sent them (the connection is closed)", n)
	}
	if c.ended {
		return value.NilValue, a.Errorf(NetError, "the other side ended the connection")
	}
	tc := tls.Client(c.c, cfg)
	ctx, stop := contextOf(env)
	defer stop()
	if c.timeout >= 0 {
		var cancel func()
		ctx, cancel = context.WithTimeout(ctx, c.timeout)
		defer cancel()
	}
	w := native.Wait(env)
	err = tc.HandshakeContext(ctx)
	w.Done()
	if err != nil {
		c.shut() // a failed handshake leaves nothing usable
		switch {
		case env.Err() != nil:
			return value.NilValue, env.Err()
		case timedOut(err) || errors.Is(err, context.DeadlineExceeded):
			return value.NilValue, a.Errorf(NetError, "no TLS handshake within %s seconds (the connection is closed)", seconds(c.timeout))
		}
		return value.NilValue, handshakeError(a, "", err)
	}
	c.c, c.servername = tc, servername
	return c.self, nil
}

// c->tls(): nil for a plain connection; else a map of what was agreed.
func tlsInfo(a native.Args, env value.Env, c *conn) (value.Value, error) {
	tc, ok := c.c.(*tls.Conn)
	if !ok {
		return value.NilValue, nil
	}
	st := tc.ConnectionState()
	m := value.NewMap()
	set := func(k string, v value.Value) { m.Set(value.MakeString(k), v) }
	set("version", value.MakeString(tls.VersionName(st.Version)))
	set("cipher", value.MakeString(tls.CipherSuiteName(st.CipherSuite)))
	name := c.servername // the client's: also for an IP address, which is not sent
	if name == "" {
		name = st.ServerName // the listener's: what the client asked for, if anything
	}
	set("servername", value.MakeString(name))
	if len(st.PeerCertificates) > 0 {
		leaf := st.PeerCertificates[0]
		set("subject", value.MakeString(leaf.Subject.String()))
		set("issuer", value.MakeString(leaf.Issuer.String()))
		set("expires", timemod.Time.Object(leaf.NotAfter))
		set("verified", value.MakeBool(len(st.VerifiedChains) > 0))
	}
	return value.MakeMap(m), nil
}

// handshakes accepts the connections of a TLS listener and does each
// handshake in the background, so that a program that connects to its
// own listener does not wait for itself; the connections whose handshake
// succeeds wait in queue for accept or onconnect. A client whose
// handshake fails is dropped: it is not the program's concern.
func (l *listener) handshakes(cfg *tls.Config) {
	for {
		c, err := l.l.Accept()
		if err != nil {
			select {
			case l.queue <- accepted{nil, err}:
			case <-l.stop:
			}
			return
		}
		go func() {
			tc := tls.Server(c, cfg)
			ctx, cancel := context.WithTimeout(context.Background(), handshakeTimeout)
			err := tc.HandshakeContext(ctx)
			cancel()
			if err != nil {
				tc.Close()
				return
			}
			select {
			case l.queue <- accepted{tc, nil}:
			case <-l.stop:
				tc.Close()
			}
		}()
	}
}
