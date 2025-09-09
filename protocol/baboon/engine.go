package baboon

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"math"
	"net"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/libraries/daze"
	"github.com/libraries/daze/lib/doa"
	"github.com/libraries/daze/lib/rate"
	"github.com/libraries/daze/protocol/ashe"
)

// Protocol baboon is the ashe protocol based on http.

// Conf is acting as package level configuration.
var Conf = struct {
	// Fake website, requests with incorrect signatures will be redirected to this address. Note that if you use the
	// baboon protocol, specify a local address whenever possible. For a cloud service provider, if it finds that you
	// are accessing an external address and sends the received data back to an in-wall connection, it may determine
	// that you are using a proxy server.
	Masker string
}{
	Masker: "https://github.com",
}

// Server implemented the baboon protocol.
type Server struct {
	Cipher []byte
	Closer io.Closer
	Limits *rate.Limits
	Listen string
	Masker string
	NextID uint32
}

// ServeMask forward the request to a fake website. From the outside, the daze server looks like a normal website.
func (s *Server) ServeMask(w http.ResponseWriter, r *http.Request) {
	req, err := http.NewRequest(r.Method, s.Masker+r.RequestURI, r.Body)
	if err != nil {
		return
	}
	req.Header = r.Header
	ret, err := http.DefaultClient.Do(req)
	if err != nil {
		return
	}
	defer ret.Body.Close()
	for k, v := range ret.Header {
		for _, e := range v {
			w.Header().Add(k, e)
		}
	}
	w.WriteHeader(ret.StatusCode)
	io.Copy(w, ret.Body)
}

// ServeDaze degenerate http protocol and run ashe protocol on it.
func (s *Server) ServeDaze(w http.ResponseWriter, r *http.Request) {
	hj, _ := w.(http.Hijacker)
	cc, rw, _ := hj.Hijack()
	io.WriteString(cc, "HTTP/1.1 200 OK\r\n")                                        // 17
	io.WriteString(cc, "Content-Length: 0\r\n")                                      // 19
	io.WriteString(cc, "Content-Type: text/plain; charset=utf-8\r\n")                // 41
	io.WriteString(cc, fmt.Sprintf("Date: %s\r\n", time.Now().Format(time.RFC1123))) // 37
	io.WriteString(cc, "X-Content-Type-Options: nosniff\r\n")                        // 33
	cli := &daze.ReadWriteCloser{
		Reader: rw,
		Writer: cc,
		Closer: cc,
	}
	spy := &ashe.Server{Cipher: s.Cipher}
	ctx := &daze.Context{Cid: atomic.AddUint32(&s.NextID, 1)}
	log.Printf("conn: %08x accept remote=%s", ctx.Cid, cc.RemoteAddr())
	rtc := &daze.ReadWriteCloser{
		Reader: io.TeeReader(cli, rate.NewLimitsWriter(s.Limits)),
		Writer: io.MultiWriter(cli, rate.NewLimitsWriter(s.Limits)),
		Closer: cli,
	}
	if err := spy.Serve(ctx, rtc); err != nil {
		log.Printf("conn: %08x  error %s", ctx.Cid, err)
	}
	log.Printf("conn: %08x closed", ctx.Cid)
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch s.Route(r) {
	case 0:
		s.ServeMask(w, r)
	case 1:
		s.ServeDaze(w, r)
	}
}

// Close listener.
func (s *Server) Close() error {
	if s.Closer != nil {
		return s.Closer.Close()
	}
	return nil
}

// Route check if the request provided the correct signature.
func (s *Server) Route(r *http.Request) int {
	authText := r.Header.Get("Authorization")
	if authText == "" {
		return 0
	}
	authData, err := hex.DecodeString(authText)
	if err != nil {
		return 0
	}
	if len(authData) != 32 {
		return 0
	}
	hash := md5.New()
	hash.Write(authData[:16])
	hash.Write(s.Cipher[:16])
	sign := hash.Sum(nil)
	for i := range 16 {
		if authData[16+i] != sign[i] {
			return 0
		}
	}
	return 1
}

// Run it.
func (s *Server) Run() error {
	l, err := net.Listen("tcp", s.Listen)
	if err != nil {
		return err
	}
	log.Println("main: listen and serve on", s.Listen)
	srv := &http.Server{Handler: s}
	s.Closer = srv
	go srv.Serve(l)
	return nil
}

// NewServer returns a new Server. Cipher is a password in string form, with no length limit.
func NewServer(listen string, cipher string) *Server {
	return &Server{
		Cipher: daze.Salt(cipher),
		Limits: rate.NewLimits(math.MaxUint32, time.Second),
		Listen: listen,
		Masker: Conf.Masker,
		NextID: uint32(math.MaxUint32),
	}
}

// Client implemented the baboon protocol.
type Client struct {
	Cipher []byte
	Server string
}

// Dial connects to the address on the named network.
func (c *Client) Dial(ctx *daze.Context, network string, address string) (io.ReadWriteCloser, error) {
	var (
		buf []byte
		err error
		req *http.Request
		srv io.ReadWriteCloser
	)
	srv, err = daze.Dial("tcp", c.Server)
	if err != nil {
		return nil, err
	}
	buf = make([]byte, 32)
	io.ReadFull(&daze.RandomReader{}, buf[:16])
	copy(buf[16:], c.Cipher[:16])
	sign := md5.Sum(buf)
	copy(buf[16:], sign[:])
	req = doa.Try(http.NewRequest("POST", "http://"+c.Server+"/sync", http.NoBody))
	req.Header.Set("Authorization", hex.EncodeToString(buf))
	req.Write(srv)
	// Discard responded header
	buf = make([]byte, 147)
	io.ReadFull(srv, buf)
	spy := &ashe.Client{Cipher: c.Cipher}
	con, err := spy.Estab(ctx, srv, network, address)
	if err != nil {
		srv.Close()
	}
	return con, err
}

// NewClient returns a new Client. Cipher is a password in string form, with no length limit.
func NewClient(server string, cipher string) *Client {
	return &Client{
		Cipher: daze.Salt(cipher),
		Server: server,
	}
}
