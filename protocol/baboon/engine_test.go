package baboon

import (
	"bytes"
	"io"
	"math/rand/v2"
	"net/http"
	"testing"

	"github.com/libraries/daze"
	"github.com/libraries/daze/lib/doa"
)

const (
	DazeServerListenOn = "127.0.0.1:28080"
	DazeTesterListenOn = "127.0.0.1:28081"
	Password           = "password"
)

func TestProtocolBaboonTCP(t *testing.T) {
	dazeTester := daze.NewTester()
	defer dazeTester.Close()
	dazeTester.ListenTCP(DazeTesterListenOn)

	dazeServer := NewServer(DazeServerListenOn, Password)
	defer dazeServer.Close()
	dazeServer.Run()

	dazeClient := NewClient(DazeServerListenOn, Password)
	ctx := &daze.Context{}
	cli := doa.Try(dazeClient.Dial(ctx, "tcp", DazeTesterListenOn))
	defer cli.Close()

	doa.Nil(dazeTester.StreamRead2(cli, rand.IntN(256)))
	doa.Nil(dazeTester.StreamWrite(cli, rand.IntN(256)))
	doa.Nil(dazeTester.StreamClose(cli))
}

func TestProtocolBaboonTCPClientClose(t *testing.T) {
	dazeTester := daze.NewTester()
	defer dazeTester.Close()
	dazeTester.ListenTCP(DazeTesterListenOn)

	dazeServer := NewServer(DazeServerListenOn, Password)
	defer dazeServer.Close()
	dazeServer.Run()

	dazeClient := NewClient(DazeServerListenOn, Password)
	ctx := &daze.Context{}
	cli := doa.Try(dazeClient.Dial(ctx, "tcp", DazeTesterListenOn))
	defer cli.Close()

	doa.Nil(cli.Close())
	doa.Doa(dazeTester.StreamRead1(cli, 1) != nil)
	doa.Doa(dazeTester.StreamWrite(cli, 1) != nil)
}

func TestProtocolBaboonTCPServerClose(t *testing.T) {
	dazeTester := daze.NewTester()
	defer dazeTester.Close()
	dazeTester.ListenTCP(DazeTesterListenOn)

	dazeServer := NewServer(DazeServerListenOn, Password)
	defer dazeServer.Close()
	dazeServer.Run()

	dazeClient := NewClient(DazeServerListenOn, Password)
	ctx := &daze.Context{}
	cli := doa.Try(dazeClient.Dial(ctx, "tcp", DazeTesterListenOn))
	defer cli.Close()

	doa.Nil(dazeTester.StreamClose(cli))
	doa.Doa(dazeTester.StreamRead1(cli, 1) != nil)
	doa.Doa(dazeTester.StreamWrite(cli, 1) != nil)
}

func TestProtocolBaboonUDP(t *testing.T) {
	dazeTester := daze.NewTester()
	defer dazeTester.Close()
	dazeTester.ListenUDP(DazeTesterListenOn)

	dazeServer := NewServer(DazeServerListenOn, Password)
	defer dazeServer.Close()
	dazeServer.Run()

	dazeClient := NewClient(DazeServerListenOn, Password)
	ctx := &daze.Context{}
	cli := doa.Try(dazeClient.Dial(ctx, "udp", DazeTesterListenOn))
	defer cli.Close()

	doa.Nil(dazeTester.PacketRead2(cli, rand.IntN(256)))
	doa.Nil(dazeTester.PacketWrite(cli, rand.IntN(256)))
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
