package daze

import (
	"bytes"
	"context"
	"os/exec"
	"testing"

	"github.com/libraries/daze/lib/doa"
)

const (
	DazeServerListenOn = "127.0.0.1:28080"
	CurlDest           = "https://www.zhihu.com"
	HostLookup         = "google.com"
)

func TestLocaleHTTP(t *testing.T) {
	locale := NewLocale(DazeServerListenOn, &Direct{})
	defer locale.Close()
	locale.Run()

	cmd := exec.Command("curl", "-x", "http://"+DazeServerListenOn, CurlDest)
	out := doa.Try(cmd.Output())
	if !bytes.Contains(out, []byte("zhihu")) {
		t.FailNow()
	}
}

func TestLocaleSocks4(t *testing.T) {
	locale := NewLocale(DazeServerListenOn, &Direct{})
	defer locale.Close()
	locale.Run()

	cmd := exec.Command("curl", "-x", "socks4://"+DazeServerListenOn, CurlDest)
	out := doa.Try(cmd.Output())
	if !bytes.Contains(out, []byte("zhihu")) {
		t.FailNow()
	}
}

func TestLocaleSocks4a(t *testing.T) {
	locale := NewLocale(DazeServerListenOn, &Direct{})
	defer locale.Close()
	locale.Run()

	cmd := exec.Command("curl", "-x", "socks4a://"+DazeServerListenOn, CurlDest)
	out := doa.Try(cmd.Output())
	if !bytes.Contains(out, []byte("zhihu")) {
		t.FailNow()
	}
}

func TestLocaleSocks5(t *testing.T) {
	locale := NewLocale(DazeServerListenOn, &Direct{})
	defer locale.Close()
	locale.Run()

	cmd := exec.Command("curl", "-x", "socks5://"+DazeServerListenOn, CurlDest)
	out := doa.Try(cmd.Output())
	if !bytes.Contains(out, []byte("zhihu")) {
		t.FailNow()
	}
}

func TestResolverDns(t *testing.T) {
	dns := ResolverDns("1.1.1.1:53")
	_, err := dns.LookupHost(context.Background(), HostLookup)
	if err != nil {
		t.FailNow()
	}
}

func TestResolverDot(t *testing.T) {
	dot := ResolverDot("1.1.1.1:853")
	_, err := dot.LookupHost(context.Background(), HostLookup)
	if err != nil {
		t.FailNow()
	}
}

func TestResolverDoh(t *testing.T) {
	doh := ResolverDoh("https://1.1.1.1/dns-query")
	_, err := doh.LookupHost(context.Background(), HostLookup)
	if err != nil {
		t.FailNow()
	}
}
