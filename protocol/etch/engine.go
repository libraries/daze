package etch

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
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
	rem net.Addr
	stm *quic.Stream
}

// Close closes the stream.
func (s *Stream) Close() error {
	s.stm.CloseRead()
	s.stm.CloseWrite()
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

// Close closes the listener and aborts established connections managed by this endpoint.
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
						if !errors.Is(err, net.ErrClosed) {
							log.Println("main:", err)
						}
						return
					}
					cli := &Stream{rem: rem, stm: stm}
					rtc := &daze.ReadWriteCloser{
						Reader: io.TeeReader(cli, rate.NewLimitsWriter(s.Limits)),
						Writer: io.MultiWriter(cli, rate.NewLimitsWriter(s.Limits)),
						Closer: cli,
					}
					cid := atomic.AddUint32(&idx, 1)
					ctx := &daze.Context{Cid: cid}
					log.Printf("conn: %08x accept remote=%s", ctx.Cid, rem)
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
	Cancel chan struct{}
	Cipher []byte
	EpQuic *quic.Endpoint
	Limits *rate.Limits
	Mux    chan *quic.Conn
	Server string
}

// Close the connection. All streams will be closed at the same time.
func (c *Client) Close() error {
	close(c.Cancel)
	return nil
}

// Dial connects to the address on the named network.
func (c *Client) Dial(ctx *daze.Context, network string, address string) (io.ReadWriteCloser, error) {
	select {
	case mux := <-c.Mux:
		cty, end := context.WithTimeout(context.Background(), daze.Conf.DialerTimeout)
		stm, err := mux.NewStream(cty)
		end()
		if err != nil {
			return nil, err
		}
		rem := net.UDPAddrFromAddrPort(mux.RemoteAddr())
		srv := &Stream{rem: rem, stm: stm}
		rtc := &daze.ReadWriteCloser{
			Reader: io.TeeReader(srv, rate.NewLimitsWriter(c.Limits)),
			Writer: io.MultiWriter(srv, rate.NewLimitsWriter(c.Limits)),
			Closer: srv,
		}
		spy := &ashe.Client{Cipher: c.Cipher}
		con, err := spy.Estab(ctx, rtc, network, address)
		if err != nil {
			rtc.Close()
			return nil, err
		}
		return con, nil
	case <-time.After(daze.Conf.DialerTimeout):
		return nil, fmt.Errorf("dial udp: %s: i/o timeout", address)
	}
}

// Run creates and maintains an established connection to the etch server.
func (c *Client) Run() {
	const (
		clientStatusClosed int = iota
		clientStatusDialFailure
		clientStatusDialSuccess
		clientStatusEstablished
		clientStatusCancel
	)
	var (
		don chan error
		err error
		mux *quic.Conn
		rtt = 0
		sid = 0
	)
	for {
		switch sid {
		case clientStatusClosed:
			cty, end := context.WithTimeout(context.Background(), daze.Conf.DialerTimeout)
			mux, err = c.EpQuic.Dial(cty, "udp", c.Server, ClientConfig.Do())
			end()
			if err != nil {
				sid = clientStatusDialFailure
			} else {
				sid = clientStatusDialSuccess
			}
		case clientStatusDialFailure:
			log.Println("etch:", err)
			select {
			case <-time.After(time.Second * time.Duration(math.Pow(2, float64(rtt)))):
				// A slow start reconnection algorithm.
				rtt = min(rtt+1, 5)
				sid = clientStatusClosed
			case <-c.Cancel:
				sid = clientStatusCancel
			}
		case clientStatusDialSuccess:
			log.Println("etch: quic init")
			rtt = 0
			don = make(chan error, 1)
			go func(mux *quic.Conn) {
				don <- mux.Wait(context.Background())
			}(mux)
			sid = clientStatusEstablished
		case clientStatusEstablished:
			select {
			case c.Mux <- mux:
			case err = <-don:
				log.Println("etch: quic done", err)
				mux.Close()
				sid = clientStatusClosed
			case <-c.Cancel:
				log.Println("etch: quic done")
				mux.Close()
				sid = clientStatusCancel
			}
		case clientStatusCancel:
			c.EpQuic.Close(context.Background())
			return
		}
	}
}

// NewClient returns a new Client. Cipher is a password in string form, with no length limit.
func NewClient(server string, cipher string) *Client {
	client := &Client{
		Cancel: make(chan struct{}),
		Cipher: daze.Salt(cipher),
		EpQuic: doa.Try(quic.Listen("udp", ":0", ClientConfig.Do())),
		Limits: rate.NewLimits(math.MaxUint32, time.Second),
		Mux:    make(chan *quic.Conn),
		Server: server,
	}
	go client.Run()
	return client
}
