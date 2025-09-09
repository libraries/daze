package baboon

import (
	"bytes"
	"encoding/binary"
	"io"
	"math/rand/v2"
	"net/http"
	"testing"

	"github.com/libraries/daze"
	"github.com/libraries/daze/lib/doa"
)

const (
	EchoServerListenOn = "127.0.0.1:28080"
	DazeServerListenOn = "127.0.0.1:28081"
	Password           = "password"
)

func TestProtocolBaboonTCP(t *testing.T) {
	dazeRemote := daze.NewTester(EchoServerListenOn)
	defer dazeRemote.Close()
	dazeRemote.TCP()

	dazeServer := NewServer(DazeServerListenOn, Password)
	defer dazeServer.Close()
	dazeServer.Run()

	dazeClient := NewClient(DazeServerListenOn, Password)
	ctx := &daze.Context{}
	cli := doa.Try(dazeClient.Dial(ctx, "tcp", EchoServerListenOn))
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

func TestProtocolBaboonTCPClientClose(t *testing.T) {
	dazeRemote := daze.NewTester(EchoServerListenOn)
	defer dazeRemote.Close()
	dazeRemote.TCP()

	dazeServer := NewServer(DazeServerListenOn, Password)
	defer dazeServer.Close()
	dazeServer.Run()

	dazeClient := NewClient(DazeServerListenOn, Password)
	ctx := &daze.Context{}
	cli := doa.Try(dazeClient.Dial(ctx, "tcp", EchoServerListenOn))
	defer cli.Close()

	cli.Close()
	doa.Doa(doa.Err(cli.Write([]byte{0x02, 0x00, 0x00, 0x00})) != nil)
	buf := make([]byte, 1)
	doa.Doa(doa.Err(io.ReadFull(cli, buf[:1])) != nil)
}

func TestProtocolBaboonTCPServerClose(t *testing.T) {
	dazeRemote := daze.NewTester(EchoServerListenOn)
	defer dazeRemote.Close()
	dazeRemote.TCP()

	dazeServer := NewServer(DazeServerListenOn, Password)
	defer dazeServer.Close()
	dazeServer.Run()

	dazeClient := NewClient(DazeServerListenOn, Password)
	ctx := &daze.Context{}
	cli := doa.Try(dazeClient.Dial(ctx, "tcp", EchoServerListenOn))
	defer cli.Close()

	doa.Try(cli.Write([]byte{0x02, 0x00, 0x00, 0x00}))
	buf := make([]byte, 1)
	doa.Doa(doa.Err(io.ReadFull(cli, buf[:1])) != nil)
}

func TestProtocolBaboonUDP(t *testing.T) {
	dazeRemote := daze.NewTester(EchoServerListenOn)
	defer dazeRemote.Close()
	dazeRemote.UDP()

	dazeServer := NewServer(DazeServerListenOn, Password)
	defer dazeServer.Close()
	dazeServer.Run()

	dazeClient := NewClient(DazeServerListenOn, Password)
	ctx := &daze.Context{}
	cli := doa.Try(dazeClient.Dial(ctx, "udp", EchoServerListenOn))
	defer cli.Close()

	doa.Try(cli.Write([]byte{0x00, 0x00, 0x00, 0x80}))
	buf := make([]byte, 128)
	doa.Try(io.ReadFull(cli, buf[:128]))
}

func TestProtocolBaboonMasker(t *testing.T) {
	dazeServer := NewServer(DazeServerListenOn, Password)
	defer dazeServer.Close()
	dazeServer.Run()

	resp := doa.Try(http.Get("http://" + DazeServerListenOn))
	body := doa.Try(io.ReadAll(resp.Body))
	resp.Body.Close()

	if resp.StatusCode != 200 {
		t.FailNow()
	}
	if len(body) == 0 {
		t.FailNow()
	}
	if !bytes.Contains(body, []byte("github")) {
		t.FailNow()
	}
}
