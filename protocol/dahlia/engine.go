package dahlia

import (
	"errors"
	"io"
	"log"
	"math"
	"net"
	"time"

	"github.com/libraries/daze"
	"github.com/libraries/daze/lib/rate"
	"github.com/libraries/daze/protocol/ashe"
)

// Dahlia is an encrypted port forwarding protocol. Unlike common port forwarding tools, it needs to configure a server
// and a client, and the communication between the server and the client is encrypted to bypass firewall detection.

// Server implemented the dahlia protocol.
type Server struct {
	Cipher []byte
	Closer io.Closer
	Listen string
	Limits *rate.Limits
	Server string
}

// Close listener. Established connections will not be closed.
func (s *Server) Close() error {
	if s.Closer != nil {
		return s.Closer.Close()
	}
	return nil
}

// Serve incoming connections. Parameter cli will be closed automatically when the function exits.
func (s *Server) Serve(ctx *daze.Context, cli io.ReadWriteCloser) error {
	spy := &ashe.Server{Cipher: s.Cipher}
	con, err := spy.Hello(cli)
	if err != nil {
		return err
	}
	srv, err := daze.Dial("tcp", s.Server)
	if err != nil {
		return err
	}
	daze.Link(con, srv)
	return nil
}

// Run it.
func (s *Server) Run() error {
	l, err := net.Listen("tcp", s.Listen)
	if err != nil {
		return err
	}
	s.Closer = l
	log.Println("main: listen and serve on", s.Listen)

	go func() {
		idx := uint32(math.MaxUint32)
		for {
			cli, err := l.Accept()
			if err != nil {
				if !errors.Is(err, net.ErrClosed) {
					log.Println("main:", err)
				}
				break
			}
			idx++
			ctx := &daze.Context{Cid: idx}
			log.Printf("conn: %08x accept remote=%s", ctx.Cid, cli.RemoteAddr())
			rtc := daze.ReadWriteCloser{
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
func NewServer(listen string, server string, cipher string) *Server {
	return &Server{
		Cipher: daze.Salt(cipher),
		Listen: listen,
		Limits: rate.NewLimits(math.MaxUint32, time.Second),
		Server: server,
	}
}

// Client implemented the dahlia protocol.
type Client struct {
	Cipher []byte
	Closer io.Closer
	Limits *rate.Limits
	Listen string
	Server string
}

// Close listener. Established connections will not be closed.
func (c *Client) Close() error {
	if c.Closer != nil {
		return c.Closer.Close()
	}
	return nil
}

// Serve incoming connections. Parameter cli will be closed automatically when the function exits.
func (c *Client) Serve(ctx *daze.Context, cli io.ReadWriteCloser) error {
	srv, err := daze.Dial("tcp", c.Server)
	if err != nil {
		return err
	}
	spy := &ashe.Client{Cipher: c.Cipher}
	con, err := spy.Hello(srv)
	if err != nil {
		srv.Close()
		return err
	}
	daze.Link(cli, con)
	return nil
}

// Run it.
func (c *Client) Run() error {
	l, err := net.Listen("tcp", c.Listen)
	if err != nil {
		return err
	}
	c.Closer = l
	log.Println("main: listen and serve on", c.Listen)

	go func() {
		idx := uint32(math.MaxUint32)
		for {
			cli, err := l.Accept()
			if err != nil {
				if !errors.Is(err, net.ErrClosed) {
					log.Println("main:", err)
				}
				break
			}
			rtc := &daze.ReadWriteCloser{
				Reader: io.TeeReader(cli, rate.NewLimitsWriter(c.Limits)),
				Writer: io.MultiWriter(cli, rate.NewLimitsWriter(c.Limits)),
				Closer: cli,
			}
			idx++
			ctx := &daze.Context{Cid: idx}
			log.Printf("conn: %08x accept remote=%s", ctx.Cid, cli.RemoteAddr())
			go func() {
				defer rtc.Close()
				if err := c.Serve(ctx, rtc); err != nil {
					log.Printf("conn: %08x  error %s", ctx.Cid, err)
				}
				log.Printf("conn: %08x closed", ctx.Cid)
			}()
		}
	}()
	return nil
}

// NewClient returns a new Client. Cipher is a password in string form, with no length limit.
func NewClient(listen string, server string, cipher string) *Client {
	return &Client{
		Cipher: daze.Salt(cipher),
		Limits: rate.NewLimits(math.MaxUint32, time.Second),
		Listen: listen,
		Server: server,
	}
}

// Middle implemented the dahlia protocol.
type Middle struct {
	Closer io.Closer
	Listen string
	Server string
}

// Close listener. Established connections will not be closed.
func (m *Middle) Close() error {
	if m.Closer != nil {
		return m.Closer.Close()
	}
	return nil
}

// Serve incoming connections. Parameter cli will be closed automatically when the function exits.
func (m *Middle) Serve(ctx *daze.Context, cli io.ReadWriteCloser) error {
	srv, err := daze.Dial("tcp", m.Server)
	if err != nil {
		return err
	}
	daze.Link(cli, srv)
	return nil
}

// Run it.
func (m *Middle) Run() error {
	l, err := net.Listen("tcp", m.Listen)
	if err != nil {
		return err
	}
	m.Closer = l
	log.Println("main: listen and serve on", m.Listen)

	go func() {
		idx := uint32(math.MaxUint32)
		for {
			cli, err := l.Accept()
			if err != nil {
				if !errors.Is(err, net.ErrClosed) {
					log.Println("main:", err)
				}
				break
			}
			idx++
			ctx := &daze.Context{Cid: idx}
			log.Printf("conn: %08x accept remote=%s", ctx.Cid, cli.RemoteAddr())
			go func() {
				defer cli.Close()
				if err := m.Serve(ctx, cli); err != nil {
					log.Printf("conn: %08x  error %s", ctx.Cid, err)
				}
				log.Printf("conn: %08x closed", ctx.Cid)
			}()
		}
	}()
	return nil
}

// NewMiddle returns a new Middle.
func NewMiddle(listen string, server string) *Middle {
	return &Middle{
		Listen: listen,
		Server: server,
	}
}
