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
	"sync/atomic"
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
			NotBefore:    time.Now().Truncate(time.Hour),
			NotAfter:     time.Now().Add(time.Hour * 24 * 365 * 10),
		}
		der := doa.Try(x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key))
		return &quic.Config{
			TLSConfig: &tls.Config{
				Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}},
				MinVersion:   tls.VersionTLS13,
				NextProtos:   []string{"ashe"},
			},
		}
	})
	ClientConfig = once.NewOnceNew(func() *quic.Config {
		return &quic.Config{
			TLSConfig: &tls.Config{
				InsecureSkipVerify: true,
				MinVersion:         tls.VersionTLS13,
				NextProtos:         []string{"ashe"},
			},
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
	EpQuic *quic.Endpoint
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
	if s.EpQuic != nil {
		return s.EpQuic.Close(context.Background())
	}
	return nil
}

// Run it.
func (s *Server) Run() error {
	l, err := quic.Listen("udp", s.Listen, ServerConfig.Do())
	if err != nil {
		return err
	}
	s.EpQuic = l
	log.Println("main: listen and serve on", s.Listen)

	go func() {
		idx := uint32(math.MaxUint32)
		for {
			con, err := l.Accept(context.Background())
			if err != nil {
				if !errors.Is(err, context.Canceled) && !errors.Is(err, net.ErrClosed) {
					log.Println("main:", err)
				}
				break
			}
			go func(con *quic.Conn) {
				defer con.Close()
				rem := net.UDPAddrFromAddrPort(con.RemoteAddr())
				for {
					stm, err := con.AcceptStream(context.Background())
					if err != nil {
						return
					}
					cid := atomic.AddUint32(&idx, 1)
					ctx := &daze.Context{Cid: cid}
					cli := &Stream{rem: rem, stm: stm}
					log.Printf("conn: %08x accept remote=%s", ctx.Cid, rem)
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
	EpQuic *quic.Endpoint
	Server string
}

// Close releases the underlying QUIC endpoint.
func (c *Client) Close() error {
	if c.EpQuic != nil {
		return c.EpQuic.Close(context.Background())
	}
	return nil
}

// Dial connects to the address on the named network.
func (c *Client) Dial(ctx *daze.Context, network string, address string) (io.ReadWriteCloser, error) {
	cty, end := context.WithTimeout(context.Background(), daze.Conf.DialerTimeout)
	defer end()
	con, err := c.EpQuic.Dial(cty, "udp", c.Server, ClientConfig.Do())
	if err != nil {
		return nil, err
	}
	stm, err := con.NewStream(cty)
	if err != nil {
		con.Close()
		return nil, err
	}
	if err := stm.Flush(); err != nil {
		con.Close()
		return nil, err
	}
	rem := net.UDPAddrFromAddrPort(con.RemoteAddr())
	srv := &Stream{con: con, rem: rem, stm: stm}
	spy := &ashe.Client{Cipher: c.Cipher}
	out, err := spy.Estab(ctx, srv, network, address)
	if err != nil {
		srv.Close()
		return nil, err
	}
	return out, nil
}

// NewClient returns a new Client. Cipher is a password in string form, with no length limit.
func NewClient(server string, cipher string) *Client {
	return &Client{
		Cipher: daze.Salt(cipher),
		EpQuic: doa.Try(quic.Listen("udp", ":0", ClientConfig.Do())),
		Server: server,
	}
}
