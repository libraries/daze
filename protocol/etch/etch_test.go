package etch

import (
	"encoding/binary"
	"io"
	"math/rand/v2"
	"testing"
	"time"

	"github.com/libraries/daze"
	"github.com/libraries/daze/lib/doa"
)

func TestProtocolEtch(t *testing.T) {
	rmt := &Tester{daze.NewTester(DazeTesterListenOn)}
	doa.Nil(rmt.Etch())
	defer rmt.Close()

	cli := doa.Try(Dial(DazeTesterListenOn))
	defer cli.Close()

	var (
		bsz = max(4, int(rand.Uint32N(256)))
		buf = make([]byte, bsz)
		cnt int
		rsz = int(rand.Uint32N(65536))
	)
	copy(buf[0:2], []byte{0x00, 0x00})
	binary.BigEndian.PutUint16(buf[2:], uint16(rsz))
	doa.Try(cli.Write(buf[:4]))
	cnt = 0
	for {
		e := min(rand.IntN(bsz+1), rsz-cnt)
		n := doa.Try(io.ReadFull(cli, buf[:e]))
		for i := range n {
			doa.Doa(buf[i] == 0x00)
		}
		cnt += n
		if cnt == rsz {
			break
		}
	}
	copy(buf[0:2], []byte{0x01, 0x00})
	binary.BigEndian.PutUint16(buf[2:], uint16(rsz))
	doa.Try(cli.Write(buf[:4]))
	for i := range bsz {
		buf[i] = 0x00
	}
	cnt = 0
	for {
		e := min(rand.IntN(bsz+1), rsz-cnt)
		n := doa.Try(cli.Write(buf[:e]))
		cnt += n
		if cnt == rsz {
			break
		}
	}
}

// TestProtocolEtchClientClose verifies that after the local side has closed the connection any further write is
// rejected with io.ErrClosedPipe, mirroring the contract documented in the mux stream tests.
func TestProtocolEtchClientClose(t *testing.T) {
	rmt := &Tester{daze.NewTester(DazeTesterListenOn)}
	doa.Nil(rmt.Etch())
	defer rmt.Close()

	cli := doa.Try(Dial(DazeTesterListenOn))
	cli.Close()
	doa.Doa(doa.Err(cli.Write([]byte{0x00, 0x00, 0x00, 0x80})) == io.ErrClosedPipe)
}

// TestProtocolEtchServerClose asks the peer to close the connection (tester cmd 0x02) and asserts that read returns
// io.EOF once the peer fin has been received and the receive buffer is drained.
func TestProtocolEtchServerClose(t *testing.T) {
	rmt := &Tester{daze.NewTester(DazeTesterListenOn)}
	doa.Nil(rmt.Etch())
	defer rmt.Close()

	cli := doa.Try(Dial(DazeTesterListenOn))
	defer cli.Close()

	doa.Try(cli.Write([]byte{0x02, 0x00, 0x00, 0x00}))
	buf := make([]byte, 1)
	doa.Doa(doa.Err(io.ReadFull(cli, buf[:1])) == io.EOF)
}

// TestProtocolEtchClientReuse opens, closes and reopens a connection against the same listener several times. It
// guards against udp 4-tuple reuse hazards in the listener routing table and verifies that the linger period left
// by a prior Close does not block a follow-up dial.
func TestProtocolEtchClientReuse(t *testing.T) {
	rmt := &Tester{daze.NewTester(DazeTesterListenOn)}
	doa.Nil(rmt.Etch())
	defer rmt.Close()

	for range 4 {
		cli := doa.Try(Dial(DazeTesterListenOn))
		doa.Try(cli.Write([]byte{0x00, 0x42, 0x00, 0x04}))
		buf := make([]byte, 4)
		doa.Try(io.ReadFull(cli, buf))
		for i := range 4 {
			doa.Doa(buf[i] == 0x42)
		}
		cli.Close()
	}
}

// TestProtocolEtchDialNobody asserts that dialing an address where no listener is running fails after the handshake
// budget rather than blocking forever.
func TestProtocolEtchDialNobody(t *testing.T) {
	srv := doa.Try(Listen("127.0.0.1:0"))
	addr := srv.Addr().String()
	srv.Close()
	old := Conf.HandshakeTimeout
	Conf.HandshakeTimeout = 200 * time.Millisecond
	defer func() { Conf.HandshakeTimeout = old }()
	_, err := Dial(addr)
	doa.Doa(err != nil)
}

// Tester wraps daze.Tester so that it serves over the etch transport instead of plain tcp or udp. The Etch method
// installs a listener that hands every new accepted connection to TCPServe, which speaks the standard tester wire
// protocol (cmd, val, count).
type Tester struct {
	*daze.Tester
}

// Etch starts the etch listener and feeds every accepted connection into TCPServe.
func (t *Tester) Etch() error {
	s, err := Listen(t.Listen)
	if err != nil {
		return err
	}
	t.Closer = s
	go func() {
		for cli := range s.Accept() {
			go t.TCPServe(cli)
		}
	}()
	return nil
}
