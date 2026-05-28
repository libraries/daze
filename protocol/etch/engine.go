package etch

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"log"
	"math"
	"math/big"
	"net"
	"sync"
	"time"

	"github.com/libraries/daze"
	"github.com/libraries/daze/lib/doa"
	"github.com/libraries/daze/lib/once"
	"github.com/libraries/daze/lib/rate"
	"github.com/libraries/daze/protocol/ashe"
	"golang.org/x/net/quic"
)

var (
	ServerConfig = once.NewOnceNew(func() *quic.Config {
		key := doa.Try(ecdsa.GenerateKey(elliptic.P256(), rand.Reader))
		tpl := &x509.Certificate{
			SerialNumber: big.NewInt(1),
			NotBefore:    time.Now().Add(-time.Hour),
			NotAfter:     time.Now().Add(time.Hour * 24 * 365 * 10),
		}
		der := doa.Try(x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key))
		return &quic.Config{
			TLSConfig: &tls.Config{
				Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}},
				MinVersion:   tls.VersionTLS13,
				NextProtos:   []string{"ashe"},
			},
			MaxBidiRemoteStreams:     math.MaxInt32,
			MaxStreamReadBufferSize:  4 * 1024 * 1024,
			MaxStreamWriteBufferSize: 4 * 1024 * 1024,
			MaxConnReadBufferSize:    4 * 1024 * 1024,
			MaxIdleTimeout:           -1,
			KeepAlivePeriod:          time.Second * 30,
		}
	})
	ClientConfig = once.NewOnceNew(func() *quic.Config {
		return &quic.Config{
			TLSConfig: &tls.Config{
				InsecureSkipVerify: true,
				MinVersion:         tls.VersionTLS13,
				NextProtos:         []string{"ashe"},
			},
			MaxBidiRemoteStreams:     math.MaxInt32,
			MaxStreamReadBufferSize:  4 * 1024 * 1024,
			MaxStreamWriteBufferSize: 4 * 1024 * 1024,
			MaxConnReadBufferSize:    4 * 1024 * 1024,
			MaxIdleTimeout:           -1,
			KeepAlivePeriod:          time.Second * 30,
		}
	})
)

// Stream wraps a quic stream as an io.ReadWriteCloser. Writes are flushed immediately so that the ashe handshake,
// which exchanges very short messages, progresses without waiting for the quic stream buffer to fill.
type Stream struct {
	con *quic.Conn
	rem net.Addr
	stm *quic.Stream
}

// Close closes the stream. If the stream owns its connection, the connection is closed as well.
func (s *Stream) Close() error {
	s.stm.CloseRead()
	s.stm.CloseWrite()
	if s.con != nil {
		return s.con.Close()
	}
	return nil
}

// Read reads up to len(p) bytes.
func (s *Stream) Read(p []byte) (int, error) {
	return s.stm.Read(p)
}

// RemoteAddr returns the remote network address.
func (s *Stream) RemoteAddr() net.Addr {
	return s.rem
}

// Write writes len(p) bytes and flushes the stream so the data reaches the wire immediately.
func (s *Stream) Write(p []byte) (int, error) {
	n, err := s.stm.Write(p)
	if err != nil {
		return n, err
	}
	if err := s.stm.Flush(); err != nil {
		return n, err
	}
	return n, nil
}

// Server implemented the ashe-over-quic protocol.
type Server struct {
	Cipher []byte
	Ep     *quic.Endpoint
	Limits *rate.Limits
	Listen string
}

// Serve incoming connections. Parameter cli will be closed automatically when the function exits.
func (s *Server) Serve(ctx *daze.Context, cli io.ReadWriteCloser) error {
	spy := &ashe.Server{Cipher: s.Cipher}
	return spy.Serve(ctx, cli)
}

// Close listener. Established connections will not be closed.
func (s *Server) Close() error {
	if s.Ep != nil {
		return s.Ep.Close(context.Background())
	}
	return nil
}

// Run it.
func (s *Server) Run() error {
	ep, err := quic.Listen("udp", s.Listen, ServerConfig.Do())
	if err != nil {
		return err
	}
	s.Ep = ep
	log.Println("main: listen and serve on", s.Listen)

	go func() {
		idx := uint32(math.MaxUint32)
		for {
			con, err := ep.Accept(context.Background())
			if err != nil {
				if !errors.Is(err, context.Canceled) && !errors.Is(err, net.ErrClosed) {
					log.Println("main:", err)
				}
				break
			}
			go func(con *quic.Conn) {
				defer con.Close()
				peer := net.UDPAddrFromAddrPort(con.RemoteAddr())
				for {
					stm, err := con.AcceptStream(context.Background())
					if err != nil {
						return
					}
					idx++
					ctx := &daze.Context{Cid: idx}
					cli := &Stream{rem: peer, stm: stm}
					log.Printf("conn: %08x accept remote=%s", ctx.Cid, peer)
					rtc := &daze.ReadWriteCloser{
						Reader: io.TeeReader(cli, rate.NewLimitsWriter(s.Limits)),
						Writer: io.MultiWriter(cli, rate.NewLimitsWriter(s.Limits)),
						Closer: cli,
					}
					go func() {
						defer rtc.Close()
						if err := s.Serve(ctx, rtc); err != nil {
							log.Printf("conn: %08x  error %s", ctx.Cid, err)
						}
						log.Printf("conn: %08x closed", ctx.Cid)
					}()
				}
			}(con)
		}
	}()

	return nil
}

// NewServer returns a new Server. Cipher is a password in string form, with no length limit.
func NewServer(listen string, cipher string) *Server {
	return &Server{
		Cipher: daze.Salt(cipher),
		Limits: rate.NewLimits(math.MaxUint32, time.Second),
		Listen: listen,
	}
}

// Client implemented the ashe-over-quic protocol.
type Client struct {
	Cipher []byte
	Server string

	mu sync.Mutex
	ep *quic.Endpoint
}

// endpoint lazily creates the shared quic endpoint that originates outbound connections. A single endpoint can
// originate many connections, so reusing it avoids burning a new udp socket for every Dial.
func (c *Client) endpoint() (*quic.Endpoint, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ep != nil {
		return c.ep, nil
	}
	// The client endpoint also needs a tls config that satisfies the "certificate or GetCertificate" requirement of
	// quic.Listen even though the endpoint never accepts inbound connections.
	ep, err := quic.Listen("udp", ":0", ClientConfig.Do())
	if err != nil {
		return nil, err
	}
	c.ep = ep
	return ep, nil
}

// Estab dials the etch server and establishes an ashe channel over it. It is the caller's responsibility to close the
// returned conn.
func (c *Client) Estab(ctx *daze.Context, network string, address string) (io.ReadWriteCloser, error) {
	ep, err := c.endpoint()
	if err != nil {
		return nil, err
	}
	dialCtx, cancel := context.WithTimeout(context.Background(), daze.Conf.DialerTimeout)
	defer cancel()
	con, err := ep.Dial(dialCtx, "udp", c.Server, ClientConfig.Do())
	if err != nil {
		return nil, err
	}
	stm, err := con.NewStream(dialCtx)
	if err != nil {
		con.Close()
		return nil, err
	}
	// NewStream is lazy: the peer does not see the stream until the first byte is written. Force a flush so the
	// server's AcceptStream returns promptly even if the ashe client decides to read before writing.
	if err := stm.Flush(); err != nil {
		con.Close()
		return nil, err
	}
	addr := net.UDPAddrFromAddrPort(con.RemoteAddr())
	srv := &Stream{con: con, rem: addr, stm: stm}
	spy := &ashe.Client{Cipher: c.Cipher}
	out, err := spy.Estab(ctx, srv, network, address)
	if err != nil {
		srv.Close()
		return nil, err
	}
	return out, nil
}

// Dial connects to the address on the named network.
func (c *Client) Dial(ctx *daze.Context, network string, address string) (io.ReadWriteCloser, error) {
	return c.Estab(ctx, network, address)
}

// NewClient returns a new Client. Cipher is a password in string form, with no length limit.
func NewClient(server string, cipher string) *Client {
	return &Client{
		Cipher: daze.Salt(cipher),
		Server: server,
	}
}
