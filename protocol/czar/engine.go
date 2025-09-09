package czar

import (
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"net"
	"time"

	"github.com/libraries/daze"
	"github.com/libraries/daze/lib/rate"
	"github.com/libraries/daze/protocol/ashe"
)

// The czar protocol is a proxy protocol built on tcp multiplexing technology. By establishing multiple tcp connections
// in one tcp channel, czar protocol effectively reduces the consumption of establishing connections between the client
// and the server:
//
// Client port: a.com ------------┐                   ┌------------ Server port: a.com
// Client port: b.com ----------┐ |                   | ┌---------- Server port: b.com
// Client port: c.com ----------+-+-- czar protocol --+-+---------- Server port: c.com
// Client port: d.com ----------┘ |                   | └---------- Server port: d.com
// Client port: e.com ------------┘                   └------------ Server port: e.com
//
// To open a stream:
//
// +-----+-----+-----+-----+
// | Sid |  0  |    Rsv    |
// +-----+-----+-----+-----+
//
// Both server and client can push data to each other.
//
// +-----+-----+-----+-----+-----+-----+
// | Sid |  1  |    Len    |    Msg    |
// +-----+-----+-----+-----+-----+-----+
//
// Close the specified stream.
//
// +-----+-----+-----+-----+
// | Sid |  2  | 0/1 | Rsv |
// +-----+-----+-----+-----+
//
// Keep alive probe and reply.
//
// +-----+-----+-----+-----+
// | 0x0 |  3  | 0/1 | Rsv |
// +-----+-----+-----+-----+

// Conf is acting as package level configuration.
var Conf = struct {
	// The newly created stream has a higher write priority.
	FastWriteDuration time.Duration
	// The duration a connection needs to be idle before mux begins sending out keep-alive probe.
	IdleProbeDuration time.Duration
	// If no data is read for more than this time, the connection is closed.
	IdleReplyDuration time.Duration
	// Packet size. Since the size of the packet header is 4, this value must be greater than 4. If the value is too
	// small, the transmission efficiency will be reduced, and if it is too large, the concurrency capability of mux
	// will be reduced.
	PacketSize int
}{
	FastWriteDuration: time.Second * 8,
	IdleProbeDuration: time.Second * 32,
	IdleReplyDuration: time.Second * 48,
	PacketSize:        2048,
}

// Server implemented the czar protocol.
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

// Close listener.
func (s *Server) Close() error {
	if s.Closer != nil {
		return s.Closer.Close()
	}
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
			rtc := &daze.ReadWriteCloser{
				Reader: io.TeeReader(cli, rate.NewLimitsWriter(s.Limits)),
				Writer: io.MultiWriter(cli, rate.NewLimitsWriter(s.Limits)),
				Closer: cli,
			}
			mux := NewMuxServer(rtc)
			go func() {
				defer mux.Close()
				for con := range mux.Accept() {
					idx++
					ctx := &daze.Context{Cid: idx}
					log.Printf("conn: %08x accept remote=%s", ctx.Cid, cli.RemoteAddr())
					go func() {
						defer con.Close()
						if err := s.Serve(ctx, con); err != nil {
							log.Printf("conn: %08x  error %s", ctx.Cid, err)
						}
						log.Printf("conn: %08x closed", ctx.Cid)
					}()
				}
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

// Client implemented the czar protocol.
type Client struct {
	Cancel chan struct{}
	Cipher []byte
	Mux    chan *Mux
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
		srv, err := mux.Open()
		if err != nil {
			return nil, err
		}
		log.Printf("czar: mux slot stream id=0x%02x", srv.idx)
		spy := &ashe.Client{Cipher: c.Cipher}
		con, err := spy.Estab(ctx, srv, network, address)
		if err != nil {
			srv.Close()
		}
		return con, err
	case <-time.After(daze.Conf.DialerTimeout):
		return nil, fmt.Errorf("dial tcp: %s: i/o timeout", address)
	}
}

// Run creates an establish connection to czar server.
func (c *Client) Run() {
	const (
		clientStatusClosed int = iota
		clientStatusDialFailure
		clientStatusDialSuccess
		clientStatusEstablished
		clientStatusCancel
	)
	var (
		err error
		mux *Mux
		rtt = 0
		sid = 0
		srv net.Conn
	)
	for {
		switch sid {
		case clientStatusClosed:
			srv, err = daze.Dial("tcp", c.Server)
			if err != nil {
				sid = clientStatusDialFailure
			} else {
				sid = clientStatusDialSuccess
			}
		case clientStatusDialFailure:
			log.Println("czar:", err)
			select {
			case <-time.After(time.Second * time.Duration(math.Pow(2, float64(rtt)))):
				// A slow start reconnection algorithm.
				rtt = min(rtt+1, 5)
				sid = clientStatusClosed
			case <-c.Cancel:
				sid = clientStatusCancel
			}
		case clientStatusDialSuccess:
			log.Println("czar: mux init")
			mux = NewMuxClient(srv)
			rtt = 0
			sid = clientStatusEstablished
		case clientStatusEstablished:
			select {
			case c.Mux <- mux:
			case <-mux.rer.Sig():
				log.Println("czar: mux done")
				mux.Close()
				sid = clientStatusClosed
			case <-c.Cancel:
				log.Println("czar: mux done")
				mux.Close()
				sid = clientStatusCancel
			}
		case clientStatusCancel:
			return
		}
	}
}

// NewClient returns a new Client. Cipher is a password in string form, with no length limit.
func NewClient(server, cipher string) *Client {
	client := &Client{
		Cancel: make(chan struct{}),
		Cipher: daze.Salt(cipher),
		Mux:    make(chan *Mux),
		Server: server,
	}
	go client.Run()
	return client
}
