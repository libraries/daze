package czar

import (
	"encoding/binary"
	"errors"
	"io"
	"log"
	"math/rand/v2"
	"net"
	"testing"

	"github.com/libraries/daze"
	"github.com/libraries/daze/lib/doa"
)

func TestProtocolCzarMux(t *testing.T) {
	rmt := &Tester{daze.NewTester(EchoServerListenOn)}
	rmt.Mux()
	defer rmt.Close()

	mux := NewMuxClient(doa.Try(net.Dial("tcp", EchoServerListenOn)))
	defer mux.Close()
	cli := doa.Try(mux.Open())
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

func TestProtocolCzarMuxStreamClientClose(t *testing.T) {
	rmt := &Tester{daze.NewTester(EchoServerListenOn)}
	rmt.Mux()
	defer rmt.Close()

	mux := NewMuxClient(doa.Try(net.Dial("tcp", EchoServerListenOn)))
	defer mux.Close()
	cli := doa.Try(mux.Open())

	cli.Close()
	doa.Doa(doa.Err(cli.Write([]byte{0x00, 0x00, 0x00, 0x80})) == io.ErrClosedPipe)
	buf := make([]byte, 1)
	doa.Doa(doa.Err(io.ReadFull(cli, buf[:1])) == io.ErrClosedPipe)
}

func TestProtocolCzarMuxStreamServerClose(t *testing.T) {
	rmt := Tester{daze.NewTester(EchoServerListenOn)}
	rmt.Mux()
	defer rmt.Close()

	mux := NewMuxClient(doa.Try(net.Dial("tcp", EchoServerListenOn)))
	defer mux.Close()
	cli := doa.Try(mux.Open())
	defer cli.Close()

	doa.Try(cli.Write([]byte{0x02, 0x00, 0x00, 0x00}))
	buf := make([]byte, 1)
	doa.Doa(doa.Err(io.ReadFull(cli, buf[:1])) == io.EOF)
}

func TestProtocolCzarMuxStreamClientReuse(t *testing.T) {
	rmt := &Tester{daze.NewTester(EchoServerListenOn)}
	rmt.Mux()
	defer rmt.Close()

	mux := NewMuxClient(doa.Try(net.Dial("tcp", EchoServerListenOn)))
	defer mux.Close()
	buf := make([]byte, 0x8000)

	cl0 := doa.Try(mux.Open())
	cl0.Write([]byte{0x00, 0x00, 0x80, 0x00})
	cl0.Close()
	for {
		idx := doa.Try(mux.idp.Get())
		mux.idp.Put(idx)
		if idx == 0x00 {
			break
		}
	}
	cl1 := doa.Try(mux.Open())
	doa.Doa(cl1.idx == 0x00)
	doa.Try(cl1.Write([]byte{0x00, 0x01, 0x80, 0x00}))
	doa.Doa(doa.Try(io.ReadFull(cl1, buf)) == 0x8000)
	for i := range 0x8000 {
		doa.Doa(buf[i] == 0x01)
	}
	cl1.Close()
}

func TestProtocolCzarMuxClientClose(t *testing.T) {
	rmt := &Tester{daze.NewTester(EchoServerListenOn)}
	rmt.Mux()
	defer rmt.Close()

	mux := NewMuxClient(doa.Try(net.Dial("tcp", EchoServerListenOn)))
	defer mux.Close()
	cli := doa.Try(mux.Open())
	defer cli.Close()

	mux.con.Close()
	doa.Doa(doa.Err(mux.Open()) != nil)
	buf := make([]byte, 1)
	doa.Doa(doa.Err(io.ReadFull(cli, buf[:1])) != nil)
	doa.Doa(doa.Err(cli.Write([]byte{0x02, 0x00, 0x00, 0x00})) != nil)
}

func TestProtocolCzarMuxServerReopen(t *testing.T) {
	rmt := &Tester{daze.NewTester(EchoServerListenOn)}
	rmt.Mux()
	defer rmt.Close()

	cli := doa.Try(net.Dial("tcp", EchoServerListenOn))
	defer cli.Close()

	cli.Write([]byte{0x00, 0x00, 0x00, 0x00})
	cli.Write([]byte{0x00, 0x00, 0x00, 0x00})
	buf := make([]byte, 1)
	doa.Doa(doa.Err(io.ReadFull(cli, buf[:1])) != nil)
}

type Tester struct {
	*daze.Tester
}

func (t *Tester) Mux() error {
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
			mux := NewMuxServer(cli)
			go func() {
				for cli := range mux.Accept() {
					go t.TCPServe(cli)
				}
			}()
		}
	}()
	return nil
}
