package etch

import (
	"encoding/binary"
	"io"
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

func TestProtocolEtchTCP(t *testing.T) {
	dazeTester := daze.NewTester(DazeTesterListenOn)
	defer dazeTester.Close()
	dazeTester.TCP()

	dazeServer := NewServer(DazeServerListenOn, Password)
	defer dazeServer.Close()
	dazeServer.Run()

	dazeClient := NewClient(DazeServerListenOn, Password)
	defer dazeClient.Close()
	ctx := &daze.Context{}
	cli := doa.Try(dazeClient.Dial(ctx, "tcp", DazeTesterListenOn))
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

func TestProtocolEtchUDP(t *testing.T) {
	dazeTester := daze.NewTester(DazeTesterListenOn)
	defer dazeTester.Close()
	dazeTester.UDP()

	dazeServer := NewServer(DazeServerListenOn, Password)
	defer dazeServer.Close()
	dazeServer.Run()

	dazeClient := NewClient(DazeServerListenOn, Password)
	defer dazeClient.Close()
	ctx := &daze.Context{}
	cli := doa.Try(dazeClient.Dial(ctx, "udp", DazeTesterListenOn))
	defer cli.Close()

	doa.Try(cli.Write([]byte{0x00, 0x00, 0x00, 0x80}))
	buf := make([]byte, 128)
	doa.Try(io.ReadFull(cli, buf[:128]))
}
