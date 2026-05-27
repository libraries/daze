package etch

import (
	"io"
	"log"
	"math"
	"time"

	"github.com/libraries/daze"
	"github.com/libraries/daze/lib/rate"
	"github.com/libraries/daze/protocol/ashe"
)

// The etch engine is the ashe protocol carried over the etch rudp transport. It exists for the same reason as the
// czar engine: to let the ashe protocol travel over a transport that is not vanilla tcp. The benefit of moving ashe
// onto rudp is that a single udp flow is much harder for middleboxes to throttle than a long-lived tcp connection, and
// the connection survives transient packet loss without giving up.

// Server implemented the ashe-over-etch protocol.
type Server struct {
	Cipher []byte
	Closer io.Closer
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
	if s.Closer != nil {
		return s.Closer.Close()
	}
	return nil
}

// Run it.
func (s *Server) Run() error {
	l, err := Listen(s.Listen)
	if err != nil {
		return err
	}
	s.Closer = l
	log.Println("main: listen and serve on", s.Listen)

	go func() {
		idx := uint32(math.MaxUint32)
		for cli := range l.Accept() {
			idx++
			ctx := &daze.Context{Cid: idx}
			log.Printf("conn: %08x accept remote=%s", ctx.Cid, cli.RemoteAddr())
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

// Client implemented the ashe-over-etch protocol.
type Client struct {
	Cipher []byte
	Server string
}

// Estab dials the etch server and establishes an ashe channel over it. It is the caller's responsibility to close the
// returned conn.
func (c *Client) Estab(ctx *daze.Context, network string, address string) (io.ReadWriteCloser, error) {
	srv, err := Dial(c.Server)
	if err != nil {
		return nil, err
	}
	spy := &ashe.Client{Cipher: c.Cipher}
	con, err := spy.Estab(ctx, srv, network, address)
	if err != nil {
		srv.Close()
		return nil, err
	}
	return con, nil
}

// Dial connects to the address on the named network.
func (c *Client) Dial(ctx *daze.Context, network string, address string) (io.ReadWriteCloser, error) {
	con, err := c.Estab(ctx, network, address)
	if err != nil {
		return nil, err
	}
	return con, nil
}

// NewClient returns a new Client. Cipher is a password in string form, with no length limit.
func NewClient(server string, cipher string) *Client {
	return &Client{
		Cipher: daze.Salt(cipher),
		Server: server,
	}
}
