package dahlia

import (
	"math/rand/v2"
	"testing"

	"github.com/libraries/daze"
	"github.com/libraries/daze/lib/doa"
)

const (
	DazeClientListenOn = "127.0.0.1:28080"
	DazeServerListenOn = "127.0.0.1:28081"
	DazeTesterListenOn = "127.0.0.1:28082"
	Password           = "password"
)

func TestProtocolDahliaTCP(t *testing.T) {
	dazeTester := daze.NewTester()
	defer dazeTester.Close()
	dazeTester.ListenTCP(DazeTesterListenOn)

	dazeServer := NewServer(DazeServerListenOn, DazeTesterListenOn, Password)
	defer dazeServer.Close()
	dazeServer.Run()

	dazeClient := NewClient(DazeClientListenOn, DazeServerListenOn, Password)
	defer dazeClient.Close()
	dazeClient.Run()
	cli := doa.Try(daze.Dial("tcp", DazeClientListenOn))
	defer cli.Close()

	doa.Nil(dazeTester.StreamRead2(cli, rand.IntN(256)))
	doa.Nil(dazeTester.StreamWrite(cli, rand.IntN(256)))
	doa.Nil(dazeTester.StreamClose(cli))
}
