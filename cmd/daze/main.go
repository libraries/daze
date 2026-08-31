package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/pprof"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/libraries/daze"
	"github.com/libraries/daze/lib/doa"
	"github.com/libraries/daze/lib/expvpp"
	"github.com/libraries/daze/lib/gracefulexit"
	"github.com/libraries/daze/lib/pretty"
	"github.com/libraries/daze/lib/rate"
	"github.com/libraries/daze/protocol/ashe"
	"github.com/libraries/daze/protocol/baboon"
	"github.com/libraries/daze/protocol/czar"
	"github.com/libraries/daze/protocol/dahlia"
	"github.com/libraries/daze/protocol/etch"
)

// Conf is acting as package level configuration.
var Conf = struct {
	PathRule string
	PathCIDR string
	Version  string
}{
	PathRule: "/rule.ls",
	PathCIDR: "/rule.cidr",
	Version:  "v1.26.7",
}

const helpMain = `Usage: daze <command> [<args>]

The most commonly used daze commands are:
  server     Start daze server
  client     Start daze client
  cidr       Generate or update rule.cidr
  fast       Run daze protocol speed test

Run 'daze <command> -h' for more information on a command.`

const helpCidr = `Usage: daze cidr <region>

Supported region:
  CN         China
  KP         Democratic People's Republic of Korea
  HK         Hong Kong Special Administrative Region of China
  ..         ...

Executing this command will update rule.cidr by remote data source. The list of all supported regions is shown on the
https://www.apnic.net/about-apnic/corporate-documents/documents/corporate/apnic-service-region/
`

func main() {
	if len(os.Args) <= 1 {
		fmt.Println(helpMain)
		return
	}
	// If daze runs in Android through termux, then we set a default dns for it. See:
	// https://stackoverflow.com/questions/38959067/dns-lookup-issue-when-running-my-go-app-in-termux
	if os.Getenv("ANDROID_ROOT") != "" {
		net.DefaultResolver = daze.ResolverDns(daze.ResolverPublic.Cloudflare.Dns)
	}
	resExec := filepath.Dir(doa.Try(os.Executable()))
	subCommand := os.Args[1]
	os.Args = os.Args[1:len(os.Args)]
	switch subCommand {
	case "server":
		var (
			flCipher = flag.String("k", "daze", "password, should be same with the one specified by client")
			flDnserv = flag.String("dns", "", "specifies the DNS, DoT or DoH server")
			flExtend = flag.String("e", "", "extend data for different protocols")
			flGpprof = flag.String("g", "", "specify an address to enable net/http/pprof")
			flLimits = flag.String("b", "", "set the maximum bandwidth in bytes per second, for example, 128k or 1.5m")
			flListen = flag.String("l", "0.0.0.0:1081", "listen address")
			flProtoc = flag.String("p", "ashe", "protocol {ashe, baboon, czar, dahlia, etch}")
		)
		flag.Parse()
		log.Println("main: server cipher is", *flCipher)
		log.Println("main: protocol is used", *flProtoc)
		if *flDnserv != "" {
			net.DefaultResolver = daze.ResolverAny(*flDnserv)
			log.Println("main: domain server is", *flDnserv)
		}
		if *flLimits != "" {
			log.Println("main: bandwidth is set", *flLimits)
		}
		switch *flProtoc {
		case "ashe":
			server := ashe.NewServer(*flListen, *flCipher)
			if *flLimits != "" {
				server.Limits = rate.NewLimits(daze.SizeParser(*flLimits), time.Second)
			}
			defer server.Close()
			doa.Nil(server.Run())
		case "baboon":
			server := baboon.NewServer(*flListen, *flCipher)
			if *flExtend != "" {
				server.Masker = *flExtend
			}
			if *flLimits != "" {
				server.Limits = rate.NewLimits(daze.SizeParser(*flLimits), time.Second)
			}
			defer server.Close()
			doa.Nil(server.Run())
		case "czar":
			server := czar.NewServer(*flListen, *flCipher)
			if *flLimits != "" {
				server.Limits = rate.NewLimits(daze.SizeParser(*flLimits), time.Second)
			}
			defer server.Close()
			doa.Nil(server.Run())
		case "dahlia":
			server := dahlia.NewServer(*flListen, *flExtend, *flCipher)
			if *flLimits != "" {
				server.Limits = rate.NewLimits(daze.SizeParser(*flLimits), time.Second)
			}
			defer server.Close()
			doa.Nil(server.Run())
		case "etch":
			server := etch.NewServer(*flListen, *flCipher)
			if *flLimits != "" {
				server.Limits = rate.NewLimits(daze.SizeParser(*flLimits), time.Second)
			}
			defer server.Close()
			doa.Nil(server.Run())
		}
		if *flGpprof != "" {
			_ = pprof.Handler
			log.Println("main: listen net/http/pprof on", *flGpprof)
			go func() { doa.Nil(http.ListenAndServe(*flGpprof, expvpp.ServeMux())) }()
		}
		// Hang prevent program from exiting.
		gracefulexit.Wait()
		log.Println("main: exit")
	case "client":
		var (
			flCidrls = flag.String("c", filepath.Join(resExec, Conf.PathCIDR), "cidr path")
			flCipher = flag.String("k", "daze", "password, should be same with the one specified by server")
			flDnserv = flag.String("dns", "", "specifies the DNS, DoT or DoH server")
			flFilter = flag.String("f", "rule", "filter {rule, remote, locale}")
			flGpprof = flag.String("g", "", "specify an address to enable net/http/pprof")
			flLimits = flag.String("b", "", "set the maximum bandwidth in bytes per second, for example, 128k or 1.5m")
			flListen = flag.String("l", "127.0.0.1:1080", "listen address")
			flProtoc = flag.String("p", "ashe", "protocol {ashe, baboon, czar, dahlia, etch}")
			flRulels = flag.String("r", filepath.Join(resExec, Conf.PathRule), "rule path")
			flServer = flag.String("s", "127.0.0.1:1081", "server address")
		)
		flag.Parse()
		log.Println("main: remote server is", *flServer)
		log.Println("main: client cipher is", *flCipher)
		log.Println("main: protocol is used", *flProtoc)
		if *flDnserv != "" {
			net.DefaultResolver = daze.ResolverAny(*flDnserv)
			log.Println("main: domain server is", *flDnserv)
		}
		if *flLimits != "" {
			log.Println("main: bandwidth is set", *flLimits)
		}
		switch *flProtoc {
		case "ashe":
			client := ashe.NewClient(*flServer, *flCipher)
			if *flLimits != "" {
				client.Limits = rate.NewLimits(daze.SizeParser(*flLimits), time.Second)
			}
			locale := daze.NewLocale(*flListen, daze.NewAimbot(client, &daze.AimbotOption{
				Type: *flFilter,
				Rule: *flRulels,
				Cidr: *flCidrls,
			}))
			defer locale.Close()
			doa.Nil(locale.Run())
		case "baboon":
			client := baboon.NewClient(*flServer, *flCipher)
			if *flLimits != "" {
				client.Limits = rate.NewLimits(daze.SizeParser(*flLimits), time.Second)
			}
			locale := daze.NewLocale(*flListen, daze.NewAimbot(client, &daze.AimbotOption{
				Type: *flFilter,
				Rule: *flRulels,
				Cidr: *flCidrls,
			}))
			defer locale.Close()
			doa.Nil(locale.Run())
		case "czar":
			client := czar.NewClient(*flServer, *flCipher)
			if *flLimits != "" {
				client.Limits = rate.NewLimits(daze.SizeParser(*flLimits), time.Second)
			}
			defer client.Close()
			locale := daze.NewLocale(*flListen, daze.NewAimbot(client, &daze.AimbotOption{
				Type: *flFilter,
				Rule: *flRulels,
				Cidr: *flCidrls,
			}))
			defer locale.Close()
			doa.Nil(locale.Run())
		case "dahlia":
			client := dahlia.NewClient(*flListen, *flServer, *flCipher)
			if *flLimits != "" {
				client.Limits = rate.NewLimits(daze.SizeParser(*flLimits), time.Second)
			}
			defer client.Close()
			doa.Nil(client.Run())
		case "etch":
			client := etch.NewClient(*flServer, *flCipher)
			if *flLimits != "" {
				client.Limits = rate.NewLimits(daze.SizeParser(*flLimits), time.Second)
			}
			defer client.Close()
			locale := daze.NewLocale(*flListen, daze.NewAimbot(client, &daze.AimbotOption{
				Type: *flFilter,
				Rule: *flRulels,
				Cidr: *flCidrls,
			}))
			defer locale.Close()
			doa.Nil(locale.Run())
		}
		if *flGpprof != "" {
			_ = pprof.Handler
			log.Println("main: listen net/http/pprof on", *flGpprof)
			go func() { doa.Nil(http.ListenAndServe(*flGpprof, expvpp.ServeMux())) }()
		}
		// Hang prevent program from exiting.
		gracefulexit.Wait()
		log.Println("main: exit")
	case "cidr":
		flag.Usage = func() {
			fmt.Fprint(flag.CommandLine.Output(), helpCidr)
			flag.PrintDefaults()
		}
		flag.Parse()
		if flag.NArg() != 1 {
			flag.Usage()
			return
		}
		cidr := daze.LoadApnic()[strings.ToUpper(flag.Arg(0))]
		name := filepath.Join(resExec, Conf.PathCIDR)
		log.Println("main: save apnic data into", name)
		f := doa.Try(os.OpenFile(name, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644))
		defer f.Close()
		for _, e := range cidr {
			fmt.Fprintln(f, "L", e.String())
		}
		log.Println("main: save apnic data done")
	case "fast":
		dazeClientListenOn := "127.0.0.1:28080"
		dazeServerListenOn := "127.0.0.1:28081"
		dazeTesterListenOn := "127.0.0.1:28082"
		bodyLength := 256 * 1024 * 1024

		dazeTester := daze.NewTester()
		doa.Nil(dazeTester.ListenTCP(dazeTesterListenOn))
		defer dazeTester.Close()

		dspdFunc := func(cli io.ReadWriteCloser) uint64 {
			tic := time.Now()
			dazeTester.StreamRead2(cli, bodyLength)
			ela := time.Since(tic)
			spd := float64(bodyLength) / ela.Seconds()
			return uint64(spd)
		}
		uspdFunc := func(cli io.ReadWriteCloser) uint64 {
			tic := time.Now()
			dazeTester.StreamWrite(cli, bodyLength)
			dazeTester.StreamRead2(cli, 1)
			ela := time.Since(tic)
			spd := float64(bodyLength) / ela.Seconds()
			return uint64(spd)
		}

		table := pretty.NewTable()
		table.Conf = []string{"<", ">", ">"}
		table.Head = []string{"protocol", "download", "upload"}
		func() {
			dazeServer := ashe.NewServer(dazeServerListenOn, "")
			defer dazeServer.Close()
			doa.Nil(dazeServer.Run())
			dazeClient := ashe.NewClient(dazeServerListenOn, "")
			cli := doa.Try(dazeClient.Dial(&daze.Context{}, "tcp", dazeTesterListenOn))
			defer cli.Close()
			dspd := daze.SizeShower(dspdFunc(cli)) + "/s"
			uspd := daze.SizeShower(uspdFunc(cli)) + "/s"
			table.Body = append(table.Body, []string{"ashe", dspd, uspd})
		}()
		func() {
			dazeServer := baboon.NewServer(dazeServerListenOn, "")
			defer dazeServer.Close()
			doa.Nil(dazeServer.Run())
			dazeClient := baboon.NewClient(dazeServerListenOn, "")
			cli := doa.Try(dazeClient.Dial(&daze.Context{}, "tcp", dazeTesterListenOn))
			defer cli.Close()
			dspd := daze.SizeShower(dspdFunc(cli)) + "/s"
			uspd := daze.SizeShower(uspdFunc(cli)) + "/s"
			table.Body = append(table.Body, []string{"baboon", dspd, uspd})
		}()
		func() {
			dazeServer := czar.NewServer(dazeServerListenOn, "")
			defer dazeServer.Close()
			doa.Nil(dazeServer.Run())
			dazeClient := czar.NewClient(dazeServerListenOn, "")
			defer dazeClient.Close()
			cli := doa.Try(dazeClient.Dial(&daze.Context{}, "tcp", dazeTesterListenOn))
			defer cli.Close()
			dspd := daze.SizeShower(dspdFunc(cli)) + "/s"
			uspd := daze.SizeShower(uspdFunc(cli)) + "/s"
			table.Body = append(table.Body, []string{"czar", dspd, uspd})
		}()
		func() {
			dazeServer := dahlia.NewServer(dazeServerListenOn, dazeTesterListenOn, "")
			defer dazeServer.Close()
			doa.Nil(dazeServer.Run())
			dazeClient := dahlia.NewClient(dazeClientListenOn, dazeServerListenOn, "")
			defer dazeClient.Close()
			doa.Nil(dazeClient.Run())
			cli := doa.Try(daze.Dial("tcp", dazeClientListenOn))
			defer cli.Close()
			dspd := daze.SizeShower(dspdFunc(cli)) + "/s"
			uspd := daze.SizeShower(uspdFunc(cli)) + "/s"
			table.Body = append(table.Body, []string{"dahlia", dspd, uspd})
		}()
		func() {
			dazeServer := etch.NewServer(dazeServerListenOn, "")
			defer dazeServer.Close()
			doa.Nil(dazeServer.Run())
			dazeClient := etch.NewClient(dazeServerListenOn, "")
			defer dazeClient.Close()
			cli := doa.Try(dazeClient.Dial(&daze.Context{}, "tcp", dazeTesterListenOn))
			defer cli.Close()
			dspd := daze.SizeShower(dspdFunc(cli)) + "/s"
			uspd := daze.SizeShower(uspdFunc(cli)) + "/s"
			table.Body = append(table.Body, []string{"etch", dspd, uspd})
		}()
		table.Print()
	case "-h", "--help":
		fmt.Println(helpMain)
	case "-v", "--version":
		fmt.Println("daze", Conf.Version)
	}
}
