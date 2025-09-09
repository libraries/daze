package daze

import (
	"bufio"
	"bytes"
	"context"
	"crypto/cipher"
	"crypto/rc4"
	"crypto/sha256"
	"crypto/tls"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"expvar"
	"fmt"
	"io"
	"log"
	"math"
	"math/bits"
	"math/rand/v2"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/libraries/daze/lib/doa"
	"github.com/libraries/daze/lib/lru"
	"github.com/libraries/daze/lib/pretty"
	"github.com/libraries/daze/lib/rate"
)

// ============================================================================
//               ___           ___           ___           ___
//              /\  \         /\  \         /\  \         /\  \
//             /::\  \       /::\  \        \:\  \       /::\  \
//            /:/\:\  \     /:/\:\  \        \:\  \     /:/\:\  \
//           /:/  \:\__\   /::\~\:\  \        \:\  \   /::\~\:\  \
//          /:/__/ \:|__| /:/\:\ \:\__\ _______\:\__\ /:/\:\ \:\__\
//          \:\  \ /:/  / \/__\:\/:/  / \::::::::/__/ \:\~\:\ \/__/
//           \:\  /:/  /       \::/  /   \:\~~\~~      \:\ \:\__\
//            \:\/:/  /        /:/  /     \:\  \        \:\ \/__/
//             \::/__/        /:/  /       \:\__\        \:\__\
//              ~~            \/__/         \/__/         \/__/
// ============================================================================

// Conf is acting as package level configuration.
var Conf = struct {
	DialerTimeout time.Duration
	RouterLruSize int
	Socks5LruSize int
}{
	DialerTimeout: time.Second * 8,
	// A single cache entry represents a single host or DNS name lookup. Make the cache as large as the maximum number
	// of clients that access your web site concurrently. Note that setting the cache size too high is a waste of
	// memory and degrades performance.
	RouterLruSize: 128,
	// The maximum number of udp connections allowed by socks5.
	Socks5LruSize: 8,
}

// Expv is a simple wrapper around the expvars package.
var Expv = struct {
	RouterCacheCall *expvar.Int
	RouterCacheHits *expvar.Int
	RouterCacheRate expvar.Func
	RouterIPNetCall *expvar.Int
	RouterIPNetTime *ExpvarAverage
}{
	RouterCacheCall: expvar.NewInt("RouterCache.Call"),
	RouterCacheHits: expvar.NewInt("RouterCache.Hits"),
	RouterCacheRate: NewExpvarPercent("RouterCache.Rate", "RouterCache.Hits", "RouterCache.Call"),
	RouterIPNetCall: expvar.NewInt("RouterIPNet.Call"),
	RouterIPNetTime: NewExpvarAverage("RouterIPNet.Time", 64),
}

// ResolverDns returns a DNS resolver.
func ResolverDns(addr string) *net.Resolver {
	return &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			d := net.Dialer{
				Timeout: Conf.DialerTimeout,
			}
			return d.DialContext(ctx, "udp", addr)
		},
	}
}

// ResolverDot returns a DoT resolver. For further information, see https://datatracker.ietf.org/doc/html/rfc7858.
func ResolverDot(addr string) *net.Resolver {
	host, _, _ := net.SplitHostPort(addr)
	conf := &tls.Config{
		ServerName:         host,
		ClientSessionCache: tls.NewLRUClientSessionCache(0),
	}
	return &net.Resolver{
		PreferGo: true,
		Dial: func(context context.Context, network, address string) (net.Conn, error) {
			d := net.Dialer{
				Timeout: Conf.DialerTimeout,
			}
			c, err := d.DialContext(context, "tcp", addr)
			if err != nil {
				return nil, err
			}
			return tls.Client(c, conf), nil
		},
	}
}

// WireConn structure can be used for DoH protocol processing.
type WireConn struct {
	Call func(b []byte) ([]byte, error)
	Data *bytes.Buffer
}

func (c *WireConn) Read(b []byte) (n int, err error)   { return c.Data.Read(b) }
func (c *WireConn) Close() error                       { return nil }
func (c *WireConn) LocalAddr() net.Addr                { return nil }
func (c *WireConn) RemoteAddr() net.Addr               { return nil }
func (c *WireConn) SetDeadline(t time.Time) error      { return nil }
func (c *WireConn) SetReadDeadline(t time.Time) error  { return nil }
func (c *WireConn) SetWriteDeadline(t time.Time) error { return nil }
func (c *WireConn) Write(b []byte) (n int, err error) {
	size := int(binary.BigEndian.Uint16(b[:2]))
	doa.Doa(size == len(b)-2)
	r, err := c.Call(b[2:])
	if err != nil {
		log.Println("main:", err)
		return len(b), nil
	}
	data := make([]byte, 2+len(r))
	binary.BigEndian.PutUint16(data[:2], uint16(len(r)))
	copy(data[2:], r)
	c.Data.Write(data)
	return len(b), nil
}

// ResolverDoh returns a DoH resolver. For further information, see https://datatracker.ietf.org/doc/html/rfc8484.
func ResolverDoh(addr string) *net.Resolver {
	return &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			conn := &WireConn{
				Call: func(b []byte) ([]byte, error) {
					resp, err := http.Post(addr, "application/dns-message", bytes.NewReader(b))
					if err != nil {
						return nil, err
					}
					defer resp.Body.Close()
					body, err := io.ReadAll(resp.Body)
					if err != nil {
						return nil, err
					}
					if resp.StatusCode != http.StatusOK {
						return nil, errors.New(string(body))
					}
					return body, nil
				},
				Data: bytes.NewBuffer([]byte{}),
			}
			return conn, nil
		},
	}
}

// ResolverDot returns a DoT resolver. Depending on the addr entered, the dns, dot or doh protocol will be used
// automatically.
//
// Dns: 1.1.1.1:53
// Dot: 1.1.1.1:853
// Doh: https://1.1.1.1/dns-query
func ResolverAny(addr string) *net.Resolver {
	switch {
	case strings.HasSuffix(addr, ":53"):
		return ResolverDns(addr)
	case strings.HasSuffix(addr, ":853"):
		return ResolverDot(addr)
	case strings.HasPrefix(addr, "https://"):
		return ResolverDoh(addr)
	}
	panic("unreachable")
}

// Link copies from src to dst and dst to src until either EOF is reached.
func Link(a, b io.ReadWriteCloser) {
	w := sync.WaitGroup{}
	w.Add(2)
	go func() {
		io.Copy(b, a)
		b.Close()
		w.Done()
	}()
	go func() {
		io.Copy(a, b)
		a.Close()
		w.Done()
	}()
	w.Wait()
}

// ReadWriteCloser is the interface that groups the basic Read, Write and Close methods.
type ReadWriteCloser struct {
	io.Reader
	io.Writer
	io.Closer
}

// Context carries infomations for a tcp connection.
type Context struct {
	Cid uint32
}

// Dialer abstracts the way to establish network connections.
type Dialer interface {
	Dial(ctx *Context, network string, address string) (io.ReadWriteCloser, error)
}

// Direct is the default dialer for connecting to an address.
type Direct struct{}

// Dial implements daze.Dialer.
func (d *Direct) Dial(ctx *Context, network string, address string) (io.ReadWriteCloser, error) {
	return Dial(network, address)
}

// Locale is the main process of daze. In most cases, it is usually deployed as a daemon on a local machine.
type Locale struct {
	Closer io.Closer
	Dialer Dialer
	Limits *rate.Limits
	Listen string
}

// ServeProxy serves traffic in HTTP Proxy/Tunnel format.
//
// Introduction:
// See https://en.wikipedia.org/wiki/Proxy_server
// See https://en.wikipedia.org/wiki/HTTP_tunnel
// See https://www.infoq.com/articles/Web-Sockets-Proxy-Servers/
func (l *Locale) ServeProxy(ctx *Context, cli io.ReadWriteCloser) error {
	cliReader := bufio.NewReader(cli)
	cli = ReadWriteCloser{
		Reader: cliReader,
		Writer: cli,
		Closer: cli,
	}
	var err error
	for {
		err = func() error {
			r, err := http.ReadRequest(cliReader)
			if err != nil {
				return err
			}

			var port string
			if r.URL.Port() == "" {
				port = "80"
			} else {
				port = r.URL.Port()
			}

			if r.Method == "CONNECT" {
				log.Printf("conn: %08x  proto format=tunnel", ctx.Cid)
			} else {
				log.Printf("conn: %08x  proto format=hproxy", ctx.Cid)
			}

			srv, err := l.Dialer.Dial(ctx, "tcp", r.URL.Hostname()+":"+port)
			if err != nil {
				return err
			}
			defer srv.Close()

			if r.Method == "CONNECT" {
				_, err := cli.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n"))
				if err != nil {
					return err
				}
				Link(cli, srv)
				return io.EOF
			}
			if r.Method == "GET" && r.Header.Get("Upgrade") == "websocket" {
				if err := r.Write(srv); err != nil {
					return err
				}
				Link(cli, srv)
				return io.EOF
			}

			srvReader := bufio.NewReader(srv)
			if err := r.Write(srv); err != nil {
				return err
			}
			s, err := http.ReadResponse(srvReader, r)
			if err != nil {
				return err
			}
			return s.Write(cli)
		}()
		if err != nil {
			break
		}
	}
	// It makes no sense to report a EOF error.
	if err == io.EOF {
		return nil
	}
	return err
}

// ServeSocks4 serves traffic in SOCKS4/SOCKS4a format.
//
// Introduction:
// See https://en.wikipedia.org/wiki/SOCKS
// See http://ftp.icm.edu.pl/packages/socks/socks4/SOCKS4.protocol
func (l *Locale) ServeSocks4(ctx *Context, cli io.ReadWriteCloser) error {
	cliReader := bufio.NewReader(cli)
	cli = ReadWriteCloser{
		Reader: cliReader,
		Writer: cli,
		Closer: cli,
	}
	var (
		dstHost   string
		dstPort   uint16
		dst       string
		err       error
		fCode     uint8
		fDstIP    = make([]byte, 4)
		fDstPort  = make([]byte, 2)
		fHostName []byte
		srv       io.ReadWriteCloser
	)
	cliReader.Discard(1)
	fCode, err = cliReader.ReadByte()
	if err != nil {
		return err
	}
	io.ReadFull(cliReader, fDstPort)
	dstPort = binary.BigEndian.Uint16(fDstPort)
	io.ReadFull(cliReader, fDstIP)
	_, err = cliReader.ReadBytes(0x00)
	if err != nil {
		return err
	}
	if bytes.Equal(fDstIP[:3], []byte{0x00, 0x00, 0x00}) && fDstIP[3] != 0x00 {
		fHostName, err = cliReader.ReadBytes(0x00)
		if err != nil {
			return err
		}
		fHostName = fHostName[:len(fHostName)-1]
		dstHost = string(fHostName)
	} else {
		dstHost = net.IP(fDstIP).String()
	}
	dst = dstHost + ":" + strconv.Itoa(int(dstPort))
	log.Printf("conn: %08x  proto format=socks4", ctx.Cid)
	switch fCode {
	case 0x01:
		srv, err = l.Dialer.Dial(ctx, "tcp", dst)
		if err != nil {
			cli.Write([]byte{0x00, 0x5b, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00})
		} else {
			defer srv.Close()
			cli.Write([]byte{0x00, 0x5a, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00})
			Link(cli, srv)
		}
		return err
	case 0x02:
		panic("unreachable")
	}
	return nil
}

// ServeSocks5 serves traffic in SOCKS5 format.
//
// Introduction:
// See https://en.wikipedia.org/wiki/SOCKS
// See https://tools.ietf.org/html/rfc1928
func (l *Locale) ServeSocks5(ctx *Context, cli io.ReadWriteCloser) error {
	cliReader := bufio.NewReader(cli)
	cli = ReadWriteCloser{
		Reader: cliReader,
		Writer: cli,
		Closer: cli,
	}
	var (
		dstHost  string
		dstPort  uint16
		dst      string
		err      error
		fAT      uint8
		fCmd     uint8
		fDstAddr []byte
		fDstPort = make([]byte, 2)
		fN       uint8
	)
	cliReader.Discard(1)
	fN = doa.Val(cliReader.ReadByte())
	cliReader.Discard(int(fN))
	cli.Write([]byte{0x05, 0x00})
	cliReader.Discard(1)
	fCmd = doa.Val(cliReader.ReadByte())
	cliReader.Discard(1)
	fAT = doa.Val(cliReader.ReadByte())
	switch fAT {
	case 0x01:
		fDstAddr = make([]byte, 4)
		io.ReadFull(cliReader, fDstAddr)
		dstHost = net.IP(fDstAddr).String()
	case 0x03:
		fN, _ = cliReader.ReadByte()
		fDstAddr = make([]byte, int(fN))
		io.ReadFull(cliReader, fDstAddr)
		dstHost = string(fDstAddr)
	case 0x04:
		fDstAddr = make([]byte, 16)
		io.ReadFull(cliReader, fDstAddr)
		dstHost = net.IP(fDstAddr).String()
	}
	_, err = io.ReadFull(cli, fDstPort)
	if err != nil {
		return err
	}
	dstPort = binary.BigEndian.Uint16(fDstPort)
	dst = net.JoinHostPort(dstHost, strconv.Itoa(int(dstPort)))
	switch fCmd {
	case 0x01:
		return l.ServeSocks5TCP(ctx, cli, dst)
	case 0x02:
		panic("unreachable")
	case 0x03:
		return l.ServeSocks5UDP(ctx, cli)
	}
	return nil
}

// ServeSocks5TCP serves socks5 TCP protocol.
func (l *Locale) ServeSocks5TCP(ctx *Context, cli io.ReadWriteCloser, dst string) error {
	log.Printf("conn: %08x  proto format=socks5", ctx.Cid)
	srv, err := l.Dialer.Dial(ctx, "tcp", dst)
	if err != nil {
		cli.Write([]byte{0x05, 0x01, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00})
	} else {
		cli.Write([]byte{0x05, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00})
		// Since the Link function will close the srv, there is no need to close it manually.
		Link(cli, srv)
	}
	return err
}

// ServeSocks5UDPRead handles the reading and forwarding of udp data.
func (l *Locale) ServeSocks5UDPRead(srv io.Reader, bnd *net.UDPConn, app *net.UDPAddr, pre []byte) error {
	var (
		buf = make([]byte, 2048)
		err error
		m   = len(pre)
		n   int
	)
	copy(buf[:m], pre)
	for {
		n, err = srv.Read(buf[m:])
		if err != nil {
			break
		}
		_, err = bnd.WriteToUDP(buf[:m+n], app)
		if err != nil {
			break
		}
	}
	return err
}

// ServeSocks5UDP serves socks5 UDP protocol.
func (l *Locale) ServeSocks5UDP(ctx *Context, cli io.ReadWriteCloser) error {
	var (
		appAddr     *net.UDPAddr
		appHeadSize int
		appHead     []byte
		appSize     int
		bndAddr     *net.UDPAddr
		bndPort     uint16
		bnd         *net.UDPConn
		buf         = make([]byte, 2048)
		cpl         = lru.New[string, io.ReadWriteCloser](Conf.Socks5LruSize)
		dstHost     string
		dstPort     uint16
		dst         string
		err         error
		srv         io.ReadWriteCloser
	)
	bndAddr = doa.Try(net.ResolveUDPAddr("udp", "127.0.0.1:0"))
	bnd = doa.Try(net.ListenUDP("udp", bndAddr))
	defer bnd.Close()
	bndPort = uint16(bnd.LocalAddr().(*net.UDPAddr).Port)
	copy(buf, []byte{0x05, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00})
	binary.BigEndian.PutUint16(buf[8:10], bndPort)
	_, err = cli.Write(buf[:10])
	if err != nil {
		return err
	}
	cpl.Drop = func(k string, v io.ReadWriteCloser) {
		v.Close()
	}

	// https://datatracker.ietf.org/doc/html/rfc1928, Page 7, UDP ASSOCIATE:
	// A UDP association terminates when the TCP connection that the UDP ASSOCIATE request arrived on terminates.
	go func() {
		io.Copy(io.Discard, cli)
		bnd.Close()
	}()

	for {
		appSize, appAddr, err = bnd.ReadFromUDP(buf)
		if err != nil {
			break
		}
		// 	+----+------+------+----------+----------+----------+
		// 	|RSV | FRAG | ATYP | DST.ADDR | DST.PORT |   DATA   |
		// 	+----+------+------+----------+----------+----------+
		// 	| 2  |  1   |  1   | Variable |    2     | Variable |
		// 	+----+------+------+----------+----------+----------+
		//  The fields in the UDP request header are:
		// 	*  RSV                               Reserved  0x0000
		// 	*  FRAG                       Current fragment number
		// 	*  ATYP          Address type of following addresses:
		// 	   *  IP V4 address:                             0x01
		// 	   *  DOMAINNAME:                                0x03
		// 	   *  IP V6 address:                             0x04
		// 	*  DST.ADDR               Desired destination address
		// 	*  DST.PORT                  Desired destination port
		// 	*  DATA                                     User data
		doa.Doa(buf[0] == 0x00)
		doa.Doa(buf[1] == 0x00)
		// Implementation of fragmentation is optional; an implementation that does not support fragmentation MUST drop
		// any datagram whose FRAG field is other than X'00'.
		doa.Doa(buf[2] == 0x00)
		switch buf[3] {
		case 0x01:
			appHeadSize = 10
		case 0x03:
			appHeadSize = int(buf[4]) + 7
		case 0x04:
			appHeadSize = 22
		}
		appHead = buf[0:appHeadSize]
		switch appHead[3] {
		case 0x01:
			dstHost = net.IP(appHead[4:8]).String()
			dstPort = binary.BigEndian.Uint16(appHead[8:10])
		case 0x03:
			l := appHead[4]
			dstHost = string(appHead[5 : 5+l])
			dstPort = binary.BigEndian.Uint16(appHead[5+l : 7+l])
		case 0x04:
			dstHost = net.IP(appHead[4:20]).String()
			dstPort = binary.BigEndian.Uint16(appHead[20:22])
		}
		dst = dstHost + ":" + strconv.Itoa(int(dstPort))
		if !cpl.Has(dst) {
			log.Printf("conn: %08x  proto format=socks5", ctx.Cid)
			srv, err = l.Dialer.Dial(ctx, "udp", dst)
			if err != nil {
				log.Printf("conn: %08x  error %s", ctx.Cid, err)
				continue
			}
			cpl.Set(dst, srv)
			go l.ServeSocks5UDPRead(srv, bnd, appAddr, appHead)
		}
		srv = cpl.Get(dst)
		_, err = srv.Write(buf[appHeadSize:appSize])
		if err != nil {
			log.Printf("conn: %08x  error %s", ctx.Cid, err)
			cpl.Del(dst)
			continue
		}
	}
	for k := range cpl.C {
		cpl.Del(k)
	}
	return nil
}

// Serve serves incoming connections and handle it with a different handler(ServeProxy/ServeSocks4/ServeSocks5).
func (l *Locale) Serve(ctx *Context, cli io.ReadWriteCloser) error {
	var (
		buf = make([]byte, 1)
		err error
	)
	cli = &ReadWriteCloser{
		Reader: io.TeeReader(cli, rate.NewLimitsWriter(l.Limits)),
		Writer: io.MultiWriter(cli, rate.NewLimitsWriter(l.Limits)),
		Closer: cli,
	}
	_, err = io.ReadFull(cli, buf)
	if err != nil {
		// There are some clients that will establish a link in advance without sending any messages so that they can
		// immediately get the connected conn when they really need it. When they leave, it makes no sense to report a
		// EOF error.
		if err == io.EOF {
			return nil
		}
		return err
	}
	cli = &ReadWriteCloser{
		Reader: io.MultiReader(bytes.NewReader(buf), cli),
		Writer: cli,
		Closer: cli,
	}
	if buf[0] == 0x05 {
		return l.ServeSocks5(ctx, cli)
	}
	if buf[0] == 0x04 {
		return l.ServeSocks4(ctx, cli)
	}
	return l.ServeProxy(ctx, cli)
}

// Close listener.
func (l *Locale) Close() error {
	if l.Closer != nil {
		return l.Closer.Close()
	}
	return nil
}

// Run it.
func (l *Locale) Run() error {
	s, err := net.Listen("tcp", l.Listen)
	if err != nil {
		return err
	}
	l.Closer = s
	log.Println("main: listen and serve on", l.Listen)

	go func() {
		idx := uint32(math.MaxUint32)
		for {
			cli, err := s.Accept()
			if err != nil {
				if !errors.Is(err, net.ErrClosed) {
					log.Println("main:", err)
				}
				break
			}
			idx++
			ctx := &Context{idx}
			log.Printf("conn: %08x accept remote=%s", ctx.Cid, cli.RemoteAddr())
			go func() {
				defer cli.Close()
				if err := l.Serve(ctx, cli); err != nil {
					log.Printf("conn: %08x  error %s", ctx.Cid, err)
				}
				log.Printf("conn: %08x closed", ctx.Cid)
			}()
		}
	}()

	return nil
}

// NewLocale returns a Locale.
func NewLocale(listen string, dialer Dialer) *Locale {
	return &Locale{
		Dialer: dialer,
		Limits: rate.NewLimits(math.MaxUint32, time.Second),
		Listen: listen,
	}
}

// ============================================================================
//               ___           ___           ___           ___
//              /\  \         /\  \         /\  \         /\  \
//             /::\  \       /::\  \       /::\  \       /::\  \
//            /:/\:\  \     /:/\:\  \     /:/\:\  \     /:/\:\  \
//           /::\~\:\  \   /:/  \:\  \   /::\~\:\  \   /:/  \:\__\
//          /:/\:\ \:\__\ /:/__/ \:\__\ /:/\:\ \:\__\ /:/__/ \:|__|
//          \/_|::\/:/  / \:\  \ /:/  / \/__\:\/:/  / \:\  \ /:/  /
//             |:|::/  /   \:\  /:/  /       \::/  /   \:\  /:/  /
//             |:|\/__/     \:\/:/  /        /:/  /     \:\/:/  /
//             |:|  |        \::/  /        /:/  /       \::/__/
//              \|__|         \/__/         \/__/         ~~
// ============================================================================

// A Road represents a host's road mode.
type Road uint32

const (
	// RoadLocale means it don't need a proxy
	RoadLocale Road = iota
	// RoadRemote means it should accessed through proxy
	RoadRemote
	// RoadFucked means it is pure rubbish
	RoadFucked
	// RoadPuzzle means ?
	RoadPuzzle
)

func (r Road) String() string {
	switch r {
	case RoadLocale:
		return "direct"
	case RoadRemote:
		return "remote"
	case RoadFucked:
		return "fucked"
	case RoadPuzzle:
		return "puzzle"
	}
	panic("unreachable")
}

// Router is a selector that will judge the host address.
type Router interface {
	// The host must be a literal IP address, or a host name that can be resolved to IP addresses.
	// Examples:
	//   Road("golang.org")
	//   Road("192.0.2.1")
	Road(ctx *Context, host string) Road
}

// RouterIPNet is a router by IPNets. It judges whether an IP or domain name is within its range.
type RouterIPNet struct {
	L []*net.IPNet
	R []*net.IPNet
	B []*net.IPNet
}

// FromFile loads a CIDR file.
func (r *RouterIPNet) FromFile(name string) {
	f := doa.Try(OpenFile(name))
	defer f.Close()
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := s.Text()
		seps := strings.Fields(line)
		if len(seps) < 2 {
			continue
		}
		_, cidr, err := net.ParseCIDR(seps[1])
		doa.Nil(err)
		switch seps[0] {
		case "#":
		case "L":
			r.L = append(r.L, cidr)
		case "R":
			r.R = append(r.R, cidr)
		case "B":
			r.B = append(r.B, cidr)
		}
	}
	doa.Nil(s.Err())
}

// Road implements daze.Router.
func (r *RouterIPNet) Road(ctx *Context, host string) Road {
	l, err := func() ([]net.IPAddr, error) {
		Expv.RouterIPNetCall.Add(1)
		t := time.Now()
		l, err := net.DefaultResolver.LookupIPAddr(context.Background(), host)
		Expv.RouterIPNetTime.Add(time.Since(t).Seconds())
		return l, err
	}()
	if err != nil {
		log.Printf("conn: %08x  error %s", ctx.Cid, err)
		return RoadPuzzle
	}
	a := l[0]
	for _, e := range r.L {
		if e.Contains(a.IP) {
			return RoadLocale
		}
	}
	for _, e := range r.R {
		if e.Contains(a.IP) {
			return RoadRemote
		}
	}
	for _, e := range r.B {
		if e.Contains(a.IP) {
			return RoadFucked
		}
	}
	return RoadPuzzle
}

// NewRouterIPNet returns a new RouterIPNet object.
func NewRouterIPNet() *RouterIPNet {
	return &RouterIPNet{
		L: LoadReservedIP(),
		R: []*net.IPNet{},
		B: []*net.IPNet{},
	}
}

// RouterRight always returns the same road.
type RouterRight struct {
	R Road
}

// Road implements daze.Router.
func (r *RouterRight) Road(ctx *Context, host string) Road {
	return r.R
}

// NewRouterRight returns a new RouterRight.
func NewRouterRight(road Road) *RouterRight {
	return &RouterRight{R: road}
}

// RouterCache cache routing results for next use.
type RouterCache struct {
	Lru *lru.Lru[string, Road]
	Raw Router
}

// Road implements daze.Router.
func (r *RouterCache) Road(ctx *Context, host string) Road {
	Expv.RouterCacheCall.Add(1)
	a, b := r.Lru.GetExists(host)
	if b {
		Expv.RouterCacheHits.Add(1)
		return a
	}
	c := r.Raw.Road(ctx, host)
	r.Lru.Set(host, c)
	return c
}

// NewRouterCache returns a new Cache object.
func NewRouterCache(r Router) *RouterCache {
	return &RouterCache{
		Lru: lru.New[string, Road](Conf.RouterLruSize),
		Raw: r,
	}
}

// RouterChain concat multiple routers in series.
type RouterChain struct {
	L []Router
}

// Road implements daze.Router.
func (r *RouterChain) Road(ctx *Context, host string) Road {
	for _, e := range r.L {
		a := e.Road(ctx, host)
		if a != RoadPuzzle {
			return a
		}
	}
	return RoadPuzzle
}

// NewRouterChain returns a new RouterChain.
func NewRouterChain(router ...Router) *RouterChain {
	return &RouterChain{
		L: router,
	}
}

// RouterRules aims to be a minimal configuration file format that's easy to read due to obvious semantics.
// There are two parts per line on the RULE file: mode and glob. mode is on the left of the space sign and glob is on
// the right. mode is a character that describes whether the host should be accessed through a proxy, and the glob is a
// glob-style string.
//
// Glob patterns:
// * h?llo matches hello, hallo and hxllo
// * h*llo matches hllo and heeeello
// * h[ae]llo matches hello and hallo, but not hillo
// * h[^e]llo matches hallo, hbllo, ... but not hello
// * h[a-b]llo matches hallo and hbllo
//
// This is a normal RULE document:
// L a.com a.a.com
// R b.com *.b.com
// B c.com
//
// L(ocale) means using locale network
// R(emote) means using remote network
// B(anned) means to block it
type RouterRules struct {
	L []string
	R []string
	B []string
}

// Road implements daze.Router.
func (r *RouterRules) Road(ctx *Context, host string) Road {
	for _, e := range r.L {
		if doa.Try(filepath.Match(e, host)) {
			return RoadLocale
		}
	}
	for _, e := range r.R {
		if doa.Try(filepath.Match(e, host)) {
			return RoadRemote
		}
	}
	for _, e := range r.B {
		if doa.Try(filepath.Match(e, host)) {
			return RoadFucked
		}
	}
	return RoadPuzzle
}

// FromFile loads a RULE file.
func (r *RouterRules) FromFile(name string) {
	f := doa.Try(OpenFile(name))
	defer f.Close()
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := s.Text()
		seps := strings.Fields(line)
		if len(seps) < 2 {
			continue
		}
		switch seps[0] {
		case "#":
		case "L":
			r.L = append(r.L, seps[1:]...)
		case "R":
			r.R = append(r.R, seps[1:]...)
		case "B":
			r.B = append(r.B, seps[1:]...)
		}
	}
	doa.Nil(s.Err())
}

// NewRouterRules returns a new RoaderRules.
func NewRouterRules() *RouterRules {
	return &RouterRules{
		L: []string{},
		R: []string{},
		B: []string{},
	}
}

// Aimbot automatically distinguish whether to use a proxy or a local network.
type Aimbot struct {
	Remote Dialer
	Locale Dialer
	Router Router
}

// Dial connects to the address on the named network.
func (s *Aimbot) Dial(ctx *Context, network string, address string) (io.ReadWriteCloser, error) {
	var (
		dst string
		err error
		rwc io.ReadWriteCloser
		tag Road
	)
	log.Printf("conn: %08x   dial network=%s address=%s", ctx.Cid, network, address)
	dst, _, err = net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	tag = s.Router.Road(ctx, dst)
	log.Printf("conn: %08x  route road=%s", ctx.Cid, tag)
	switch tag {
	case RoadLocale:
		rwc, err = s.Locale.Dial(ctx, network, address)
	case RoadRemote:
		rwc, err = s.Remote.Dial(ctx, network, address)
	case RoadFucked:
		err = fmt.Errorf("conn: %s has been blocked", dst)
	case RoadPuzzle:
		rwc, err = s.Remote.Dial(ctx, network, address)
	}
	if err == nil {
		log.Printf("conn: %08x  estab", ctx.Cid)
	}
	return rwc, err
}

// AimbotOption provides configuration for quick initialization of Aimbot.
type AimbotOption struct {
	Type string
	Rule string
	Cidr string
}

// NewAimbot returns a new Aimbot.
func NewAimbot(client Dialer, option *AimbotOption) *Aimbot {
	router := func() Router {
		if option.Type == "locale" {
			routerRight := NewRouterRight(RoadLocale)
			return routerRight
		}
		if option.Type == "remote" {
			routerLocal := NewRouterIPNet()
			routerRight := NewRouterRight(RoadRemote)
			routerChain := NewRouterChain(routerLocal, routerRight)
			routerCache := NewRouterCache(routerChain)
			return routerCache
		}
		if option.Type == "rule" {
			log.Println("main: load rule", option.Rule)
			routerRules := NewRouterRules()
			routerRules.FromFile(option.Rule)
			log.Println("main: size is", len(routerRules.L)+len(routerRules.R)+len(routerRules.B))

			log.Println("main: load rule", option.Cidr)
			routerLocal := NewRouterIPNet()
			routerLocal.FromFile(option.Cidr)
			log.Println("main: size is", len(routerLocal.L)+len(routerLocal.R)+len(routerLocal.B))

			routerRight := NewRouterRight(RoadRemote)
			routerChain := NewRouterChain(routerRules, routerLocal, routerRight)
			routerCache := NewRouterCache(routerChain)
			return routerCache
		}
		panic("unreachable")
	}()
	return &Aimbot{
		Remote: client,
		Locale: &Direct{},
		Router: router,
	}
}

// ============================================================================
//               ___           ___           ___           ___
//              /\  \         /\  \         /\  \         /\__\
//              \:\  \       /::\  \       /::\  \       /:/  /
//               \:\  \     /:/\:\  \     /:/\:\  \     /:/  /
//               /::\  \   /:/  \:\  \   /:/  \:\  \   /:/  /
//              /:/\:\__\ /:/__/ \:\__\ /:/__/ \:\__\ /:/__/
//             /:/  \/__/ \:\  \ /:/  / \:\  \ /:/  / \:\  \
//            /:/  /       \:\  /:/  /   \:\  /:/  /   \:\  \
//           /:/  /         \:\/:/  /     \:\/:/  /     \:\  \
//          /:/  /           \::/  /       \::/  /       \:\__\
//          \/__/             \/__/         \/__/         \/__/
// ============================================================================

// Check interface implementation.
var (
	_ Dialer = (*Aimbot)(nil)
	_ Dialer = (*Direct)(nil)
	_ Router = (*RouterCache)(nil)
	_ Router = (*RouterChain)(nil)
	_ Router = (*RouterIPNet)(nil)
	_ Router = (*RouterRight)(nil)
	_ Router = (*RouterRules)(nil)
)

// Dial connects to the address on the named network.
func Dial(network string, address string) (net.Conn, error) {
	d := net.Dialer{
		Timeout: Conf.DialerTimeout,
	}
	return d.Dial(network, address)
}

// ExpvarAverage is a structure to maintain a running average using expvar.Float.
type ExpvarAverage struct {
	F *expvar.Float
	L float64
}

// Adds a new value to the running average. This is not strictly concurrency-safe, but it won't have much impact on the
// data.
func (e *ExpvarAverage) Add(value float64) {
	e.F.Add((value - e.F.Value()) / e.L)
}

// NewExpvarAverage creates and initializes a new ExpvarAverage instance.
func NewExpvarAverage(name string, length int) *ExpvarAverage {
	return &ExpvarAverage{
		F: expvar.NewFloat(name),
		L: float64(length),
	}
}

// NewExpvarPercent creates a new expvar.Func that calculates the ratio of two expvar.Int or expvar.Float metrics.
func NewExpvarPercent(name string, n string, d string) expvar.Func {
	f := expvar.Func(func() any {
		v := doa.Try(strconv.ParseFloat(expvar.Get(n).String(), 64))
		w := doa.Try(strconv.ParseFloat(expvar.Get(d).String(), 64))
		return float64(v) / float64(max(1, w))
	})
	expvar.Publish(name, f)
	return f
}

// GravityReader wraps an io.Reader with RC4 crypto.
func GravityReader(r io.Reader, k []byte) io.Reader {
	cr := doa.Try(rc4.NewCipher(k))
	return cipher.StreamReader{S: cr, R: r}
}

// GravityWriter wraps an io.Writer with RC4 crypto.
func GravityWriter(w io.Writer, k []byte) io.Writer {
	cw := doa.Try(rc4.NewCipher(k))
	return cipher.StreamWriter{S: cw, W: w}
}

// Gravity double, happiness double.
func Gravity(conn io.ReadWriteCloser, k []byte) io.ReadWriteCloser {
	cr := doa.Try(rc4.NewCipher(k))
	cw := doa.Try(rc4.NewCipher(k))
	return &ReadWriteCloser{
		Reader: cipher.StreamReader{S: cr, R: conn},
		Writer: cipher.StreamWriter{S: cw, W: conn},
		Closer: conn,
	}
}

// OpenFile select the appropriate method to open the file based on the incoming args automatically.
//
// Examples:
// OpenFile("/etc/hosts")
// OpenFile("https://raw.githubusercontent.com/libraries/daze/master/README.md")
func OpenFile(name string) (io.ReadCloser, error) {
	if strings.HasPrefix(name, "http://") || strings.HasPrefix(name, "https://") {
		resp, err := http.Get(name)
		if err != nil {
			return nil, err
		}
		return resp.Body, nil
	}
	return os.Open(name)
}

// RandomReader is a simple random number generator. Note that it is not cryptographically secure, but for daze, the
// randomness it provides is enough.
type RandomReader struct{}

// Read implements io.Reader.
func (r *RandomReader) Read(p []byte) (int, error) {
	for i := range len(p) {
		p[i] = byte(rand.Uint64())
	}
	return len(p), nil
}

// Salt converts the stupid password passed in by the user to 32-sized byte array.
func Salt(s string) []byte {
	h := sha256.Sum256([]byte(s))
	return h[:]
}

// SizeParser converts a string like "1K", "1M", or "1G" to bytes as uint64. It expects the input string to end with a
// unit (K, M, or G) and panics if the unit is invalid. The number part can be a float (e.g., "1.5M") and is converted
// to bytes based on the unit.
func SizeParser(s string) uint64 {
	f := doa.Try(strconv.ParseFloat(s[:len(s)-1], 64))
	u := strings.ToLower(s[len(s)-1:])
	switch u {
	case "k":
		return uint64(f * 1024)
	case "m":
		return uint64(f * 1024 * 1024)
	case "g":
		return uint64(f * 1024 * 1024 * 1024)
	}
	panic("unreachable")
}

// ============================================================================
//                 ___           ___           ___           ___
//                /\  \         /\  \         /\  \         /\  \
//               /::\  \       /::\  \        \:\  \       /::\  \
//              /:/\:\  \     /:/\:\  \        \:\  \     /:/\:\  \
//             /:/  \:\__\   /::\~\:\  \       /::\  \   /::\~\:\  \
//            /:/__/ \:|__| /:/\:\ \:\__\     /:/\:\__\ /:/\:\ \:\__\
//            \:\  \ /:/  / \/__\:\/:/  /    /:/  \/__/ \/__\:\/:/  /
//             \:\  /:/  /       \::/  /    /:/  /           \::/  /
//              \:\/:/  /        /:/  /     \/__/            /:/  /
//               \::/__/        /:/  /                      /:/  /
//                ~~            \/__/                       \/__/
// ============================================================================

// LoadApnic loads remote resource. APNIC is the Regional Internet Registry administering IP addresses for the Asia
// Pacific.
func LoadApnic() map[string][]*net.IPNet {
	url := "http://ftp.apnic.net/apnic/stats/apnic/delegated-apnic-latest"
	log.Println("main: load apnic data from", url)
	rep := doa.Try(http.Get(url))
	defer rep.Body.Close()
	f := io.TeeReader(rep.Body, pretty.NewProgressWriter(uint64(rep.ContentLength)))
	r := map[string][]*net.IPNet{}
	s := bufio.NewScanner(f)
	for s.Scan() {
		line := s.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		seps := strings.Split(line, "|")
		if seps[1] == "*" {
			continue
		}
		switch seps[2] {
		case "ipv4":
			sep4 := doa.Try(strconv.ParseUint(seps[4], 0, 32))
			// Determine whether it is a power of 2
			doa.Doa(sep4&(sep4-1) == 0)
			mask := bits.LeadingZeros64(sep4) - 31
			_, cidr, err := net.ParseCIDR(fmt.Sprintf("%s/%d", seps[3], mask))
			doa.Nil(err)
			r[seps[1]] = append(r[seps[1]], cidr)
		case "ipv6":
			seps := strings.Split(line, "|")
			sep4 := seps[4]
			_, cidr, err := net.ParseCIDR(fmt.Sprintf("%s/%s", seps[3], sep4))
			doa.Nil(err)
			r[seps[1]] = append(r[seps[1]], cidr)
		}
	}
	log.Println("main: load apnic done")
	return r
}

// LoadReservedIP loads reserved ip addresses.
//
// Introduction:
// See https://en.wikipedia.org/wiki/Reserved_IP_addresses
func LoadReservedIP() []*net.IPNet {
	r := []*net.IPNet{}
	for _, e := range [][2]string{
		// IPv4
		{"00000000", "FF000000"},
		{"0A000000", "FF000000"},
		{"7F000000", "FF000000"},
		{"A9FE0000", "FFFF0000"},
		{"AC100000", "FFF00000"},
		{"C0000000", "FFFFFFF8"},
		{"C00000AA", "FFFFFFFE"},
		{"C0000200", "FFFFFF00"},
		{"C0A80000", "FFFF0000"},
		{"C6120000", "FFFE0000"},
		{"C6336400", "FFFFFF00"},
		{"CB007100", "FFFFFF00"},
		{"F0000000", "F0000000"},
		{"FFFFFFFF", "FFFFFFFF"},
		// IPv6
		{"00000000000000000000000000000000", "FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFF"},
		{"00000000000000000000000000000001", "FFFFFFFFFFFFFFFFFFFFFFFFFFFFFFFF"},
		{"01000000000000000000000000000000", "FFFFFFFFFFFFFFFF0000000000000000"},
		{"0064FF9B000000000000000000000000", "FFFFFFFFFFFFFFFFFFFFFFFF00000000"},
		{"20010000000000000000000000000000", "FFFFFFFF000000000000000000000000"},
		{"20010010000000000000000000000000", "FFFFFFF0000000000000000000000000"},
		{"20010020000000000000000000000000", "FFFFFFF0000000000000000000000000"},
		{"20010DB8000000000000000000000000", "FFFFFFFF000000000000000000000000"},
		{"20020000000000000000000000000000", "FFFF0000000000000000000000000000"},
		{"FC000000000000000000000000000000", "FE000000000000000000000000000000"},
		{"FE800000000000000000000000000000", "FFC00000000000000000000000000000"},
		{"FF000000000000000000000000000000", "FF000000000000000000000000000000"},
	} {
		i := doa.Try(hex.DecodeString(e[0]))
		m := doa.Try(hex.DecodeString(e[1]))
		r = append(r, &net.IPNet{IP: i, Mask: m})
	}
	return r
}

// ============================================================================
//              ___           ___           ___           ___
//             /\  \         /\  \         /\  \         /\  \
//             \:\  \       /::\  \       /::\  \        \:\  \
//              \:\  \     /:/\:\  \     /:/\ \  \        \:\  \
//              /::\  \   /::\~\:\  \   _\:\~\ \  \       /::\  \
//             /:/\:\__\ /:/\:\ \:\__\ /\ \:\ \ \__\     /:/\:\__\
//            /:/  \/__/ \:\~\:\ \/__/ \:\ \:\ \/__/    /:/  \/__/
//           /:/  /       \:\ \:\__\    \:\ \:\__\     /:/  /
//           \/__/         \:\ \/__/     \:\/:/  /     \/__/
//                          \:\__\        \::/  /            http://patorjk.com
//                           \/__/         \/__/                     Isometric1
// ============================================================================

// A remote server for testing.
type Tester struct {
	Listen string
	Closer io.Closer
}

// Run it on TCP.
func (t *Tester) TCP() error {
	s, err := net.Listen("tcp", t.Listen)
	if err != nil {
		return err
	}
	t.Closer = s
	go func() {
		for {
			cli, err := s.Accept()
			if err != nil {
				if !errors.Is(err, net.ErrClosed) {
					log.Println("main:", err)
				}
				break
			}
			go t.TCPServe(cli)
		}
	}()
	return nil
}

// TCPServe serves incoming connections.
func (t *Tester) TCPServe(cli io.ReadWriteCloser) {
	buf := make([]byte, 2048)
	for {
		_, err := io.ReadFull(cli, buf[:4])
		if err != nil {
			break
		}
		cmd := buf[0]
		switch cmd {
		case 0:
			val := buf[1]
			cnt := int(binary.BigEndian.Uint16(buf[2:4]))
			for i := range len(buf) {
				buf[i] = val
			}
			for cnt >= len(buf) {
				cli.Write(buf)
				cnt -= len(buf)
			}
			if cnt != 0 {
				cli.Write(buf[:cnt])
			}
		case 1:
			val := buf[1]
			cnt := int(binary.BigEndian.Uint16(buf[2:4]))
			for cnt >= len(buf) {
				io.ReadFull(cli, buf)
				for i := range len(buf) {
					doa.Doa(buf[i] == val)
				}
				cnt -= len(buf)
			}
			if cnt != 0 {
				io.ReadFull(cli, buf[:cnt])
				for i := range cnt {
					doa.Doa(buf[i] == val)
				}
			}
		case 2:
			cli.Close()
		}
	}
}

// Run it on UDP.
func (t *Tester) UDP() error {
	addr := doa.Try(net.ResolveUDPAddr("udp", t.Listen))
	conn := doa.Try(net.ListenUDP("udp", addr))
	t.Closer = conn
	go t.UDPServe(conn)
	return nil
}

// UDPServe serves incoming connections.
func (t *Tester) UDPServe(cli *net.UDPConn) error {
	buf := make([]byte, 2048)
	for {
		_, addr, err := cli.ReadFromUDP(buf)
		if err != nil {
			break
		}
		cmd := buf[0]
		switch cmd {
		case 0:
			val := buf[1]
			cnt := binary.BigEndian.Uint16(buf[2:4])
			for i := range cnt {
				buf[i] = val
			}
			doa.Try(cli.WriteToUDP(buf[:cnt], addr))
		case 1:
			val := buf[1]
			cnt := binary.BigEndian.Uint16(buf[2:4])
			for i := range cnt {
				doa.Doa(buf[4+i] == val)
			}
		case 2:
			cli.Close()
		}
	}
	return nil
}

// Close listener.
func (t *Tester) Close() error {
	if t.Closer != nil {
		return t.Closer.Close()
	}
	return nil
}

// NewTester returns a new Tester.
func NewTester(listen string) *Tester {
	return &Tester{
		Listen: listen,
	}
}
