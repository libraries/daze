package czar

import (
	"io"
	"math/rand/v2"
	"net"
	"testing"
	"time"

	"github.com/libraries/daze"
	"github.com/libraries/daze/lib/doa"
)

func TestProtocolCzarMux(t *testing.T) {
	rmt := &Tester{daze.NewTester()}
	rmt.ListenMux(DazeTesterListenOn)
	defer rmt.Close()

	mux := NewMuxClient(doa.Try(net.Dial("tcp", DazeTesterListenOn)))
	defer mux.Close()
	cli := doa.Try(mux.Open())
	defer cli.Close()

	doa.Nil(rmt.StreamRead2(cli, rand.IntN(256)))
	doa.Nil(rmt.StreamWrite(cli, rand.IntN(256)))
	doa.Nil(rmt.StreamClose(cli))
}

func TestProtocolCzarMuxStreamClientClose(t *testing.T) {
	rmt := &Tester{daze.NewTester()}
	rmt.ListenMux(DazeTesterListenOn)
	defer rmt.Close()

	mux := NewMuxClient(doa.Try(net.Dial("tcp", DazeTesterListenOn)))
	defer mux.Close()
	cli := doa.Try(mux.Open())
	defer cli.Close()

	doa.Nil(cli.Close())
	doa.Doa(rmt.StreamRead1(cli, 1) == io.ErrClosedPipe)
	doa.Doa(rmt.StreamWrite(cli, 1) == io.ErrClosedPipe)
}

func TestProtocolCzarMuxStreamServerClose(t *testing.T) {
	rmt := Tester{daze.NewTester()}
	rmt.ListenMux(DazeTesterListenOn)
	defer rmt.Close()

	mux := NewMuxClient(doa.Try(net.Dial("tcp", DazeTesterListenOn)))
	defer mux.Close()
	cli := doa.Try(mux.Open())
	defer cli.Close()

	doa.Nil(rmt.StreamClose(cli))
	doa.Doa(rmt.StreamRead1(cli, 1) == io.EOF)
	doa.Doa(rmt.StreamWrite(cli, 1) == io.ErrClosedPipe)
}

func TestProtocolCzarMuxStreamClientReuse(t *testing.T) {
	rmt := &Tester{daze.NewTester()}
	rmt.ListenMux(DazeTesterListenOn)
	defer rmt.Close()

	mux := NewMuxClient(doa.Try(net.Dial("tcp", DazeTesterListenOn)))
	defer mux.Close()

	cl0 := doa.Try(mux.Open())
	doa.Nil(rmt.StreamRead2(cl0, rand.IntN(256)))
	doa.Nil(rmt.StreamWrite(cl0, rand.IntN(256)))
	doa.Nil(rmt.StreamClose(cl0))
	for {
		time.Sleep(time.Millisecond * 100)
		idx := doa.Try(mux.idp.Get())
		mux.idp.Put(idx)
		if idx == 0x00 {
			break
		}
	}
	cl1 := doa.Try(mux.Open())
	doa.Doa(cl1.idx == 0x00)
	doa.Nil(rmt.StreamRead2(cl1, rand.IntN(256)))
	doa.Nil(rmt.StreamWrite(cl1, rand.IntN(256)))
	doa.Nil(rmt.StreamClose(cl1))
}

func TestProtocolCzarMuxClientClose(t *testing.T) {
	rmt := &Tester{daze.NewTester()}
	rmt.ListenMux(DazeTesterListenOn)
	defer rmt.Close()

	mux := NewMuxClient(doa.Try(net.Dial("tcp", DazeTesterListenOn)))
	defer mux.Close()
	cli := doa.Try(mux.Open())
	defer cli.Close()

	mux.con.Close()
	doa.Doa(doa.Err(mux.Open()) != nil)
	doa.Doa(rmt.StreamRead1(cli, 1) != nil)
	doa.Doa(rmt.StreamWrite(cli, 1) != nil)
}

func TestProtocolCzarMuxServerReopen(t *testing.T) {
	rmt := &Tester{daze.NewTester()}
	rmt.ListenMux(DazeTesterListenOn)
	defer rmt.Close()

	cli := doa.Try(net.Dial("tcp", DazeTesterListenOn))
	defer cli.Close()

	mph := make([]byte, 4)
	mph[0] = 0x00 // Cmd open stream
	mph[1] = 0x00 // Sid
	cli.Write(mph)
	cli.Write(mph)
	doa.Doa(rmt.StreamRead1(cli, 1) != nil)
	doa.Doa(rmt.StreamWrite(cli, 1) != nil)
}

type Tester struct {
	*daze.Tester
}

func (t *Tester) ListenMux(listen string) error {
	s := doa.Try(net.Listen("tcp", listen))
	t.Closer = s
	go func() {
		for {
			c, err := s.Accept()
			if err != nil {
				break
			}
			m := NewMuxServer(c)
			go func() {
				defer m.Close()
				for i := range m.Accept() {
					go t.HandleTCP(i)
				}
			}()
		}
	}()
	return nil
}
