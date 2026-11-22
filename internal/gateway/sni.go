package gateway

import (
	"bytes"
	"crypto/tls"
	"errors"
	"io"
	"net"
	"time"
)

var errHelloRead = errors.New("client hello read")

// peekClientHello reads the TLS ClientHello from conn and returns the SNI
// server name plus all bytes consumed, so the connection can be replayed
// (passthrough) or terminated afterwards. It lets crypto/tls do the parsing:
// the handshake is aborted from GetConfigForClient, right after the hello was
// parsed, and the connection is read-only so nothing is ever written back.
func peekClientHello(conn net.Conn) (string, []byte, error) {
	var buf bytes.Buffer
	var sni string
	var seen bool
	err := tls.Server(readOnlyConn{r: io.TeeReader(conn, &buf)}, &tls.Config{
		GetConfigForClient: func(h *tls.ClientHelloInfo) (*tls.Config, error) {
			sni, seen = h.ServerName, true
			return nil, errHelloRead
		},
	}).Handshake()
	if !seen {
		if err == nil {
			err = errors.New("no client hello")
		}
		return "", buf.Bytes(), err
	}
	return sni, buf.Bytes(), nil
}

// readOnlyConn feeds a TLS server from a reader and refuses writes.
type readOnlyConn struct{ r io.Reader }

func (c readOnlyConn) Read(p []byte) (int, error)     { return c.r.Read(p) }
func (readOnlyConn) Write([]byte) (int, error)        { return 0, io.ErrClosedPipe }
func (readOnlyConn) Close() error                     { return nil }
func (readOnlyConn) LocalAddr() net.Addr              { return nil }
func (readOnlyConn) RemoteAddr() net.Addr             { return nil }
func (readOnlyConn) SetDeadline(time.Time) error      { return nil }
func (readOnlyConn) SetReadDeadline(time.Time) error  { return nil }
func (readOnlyConn) SetWriteDeadline(time.Time) error { return nil }

// prefixConn replays already-read bytes before reading from the connection.
type prefixConn struct {
	net.Conn
	prefix []byte
}

func (c *prefixConn) Read(p []byte) (int, error) {
	if len(c.prefix) > 0 {
		n := copy(p, c.prefix)
		c.prefix = c.prefix[n:]
		return n, nil
	}
	return c.Conn.Read(p)
}

// chanListener is a net.Listener fed with already-accepted connections
// (used to serve TLS-terminated connections with a normal http.Server).
type chanListener struct {
	c      chan net.Conn
	addr   net.Addr
	closed chan struct{}
}

func newChanListener(addr net.Addr) *chanListener {
	return &chanListener{c: make(chan net.Conn), addr: addr, closed: make(chan struct{})}
}

func (l *chanListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.c:
		return c, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *chanListener) push(c net.Conn) bool {
	select {
	case l.c <- c:
		return true
	case <-l.closed:
		return false
	}
}

func (l *chanListener) Close() error {
	select {
	case <-l.closed:
	default:
		close(l.closed)
	}
	return nil
}

func (l *chanListener) Addr() net.Addr { return l.addr }
