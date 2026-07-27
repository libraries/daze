package ashe

import (
	"math/rand/v2"
	"testing"

	"github.com/libraries/daze"
	"github.com/libraries/daze/lib/doa"
)

const (
	DazeServerListenOn = "127.0.0.1:28080"
	DazeTesterListenOn = "127.0.0.1:28081"
	Password           = "password"
)

func TestProtocolAsheTCP(t *testing.T) {
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

func TestProtocolAsheTCPClientClose(t *testing.T) {
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

func TestProtocolAsheTCPServerClose(t *testing.T) {
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

func TestProtocolAsheUDP(t *testing.T) {
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
