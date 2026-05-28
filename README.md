# Daze

![img](./res/daze.jpg)

Daze is a software that helps you pass through firewalls, in other words, a proxy. It uses a simple yet efficient protocol, ensuring that you never get detected or blocked.

## Introduction

Daze is designed as a single-file application. First, compile or [download](https://github.com/libraries/daze/releases) daze:

```sh
$ git clone https://github.com/libraries/daze
$ cd daze

# On Linux or macOS
$ cmd/develop.sh
# On Windows
$ cmd/develop.ps1
```

The build results will be saved in the `bin` directory. You can keep this directory, and all other files are not required.

Daze is dead simple to use:

```sh
# Server side
# You need a machine that can access the Internet, and enter the following command:
$ daze server -l 0.0.0.0:1081 -k $PASSWORD

# Client side
# Use the following command to link your server(replace $SERVER with your server IP):
$ daze client -s $SERVER:1081 -k $PASSWORD
# Now, you are free to visit the Internet
$ curl -x http://127.0.0.1:1080    http://google.com
$ curl -x http://127.0.0.1:1080    https://google.com
$ curl -x socks4://127.0.0.1:1080  https://google.com
$ curl -x socks4a://127.0.0.1:1080 https://google.com
$ curl -x socks5://127.0.0.1:1080  https://google.com
$ curl -x socks5h://127.0.0.1:1080 https://google.com
```

Daze is still under development. You should make sure that the server and client have the same version number (check with the `daze ver` command) or commit hash.

## Deployment

Daze is implemented in pure Go language, so it can run on almost any operating system. The following are some of the browsers/operating systems commonly used by me:

0. Android. Cross-compile daze for android: `GOOS=android GOARCH=arm64 go build -o daze github.com/libraries/daze/cmd/daze`. Push the compiled file to the phone, You can use [adb](https://developer.android.com/studio/command-line/adb) or create an http server and download daze with `wget` in [termux](https://play.google.com/store/apps/details?id=com.termux&hl=en). Run `daze client -l 127.0.0.1:1080 ...` in the termux. Set the proxy for phone: WLAN -> Settings -> Proxy -> Fill in `127.0.0.1:1080`
0. Chrome. Chrome does not support setting proxies, so a third-party plugin must be used. [Proxy SwitchyOmega](https://chrome.google.com/webstore/detail/proxy-switchyomega/padekgcemlokbadohgkifijomclgjgif?hl=en) works very well.
0. Firefox can configure a proxy in `Connection Settings` -> `Manual proxy configuration` -> `SOCKSv5 Host=127.0.0.1` and `Port=1080`. If you see an option `Use remote DNS` on the page, check it.

## Configuration: Bandwidth Limiter

You can limit the maximum bandwidth used by daze. Generally speaking, for daze server, it is recommended to set the bandwidth to a value slightly smaller than the physical bandwidth.

```sh
# For daze server, set -b 320k if your physical bandwidth is 3M, where 320 = 3 * 1024 / 8 - 64.
$ daze server ... -b 320k
# For daze client, in most cases no configuration is necessary.
$ daze client ...
```

## Configuration: DNS

The DNS server and DNS protocol used by daze can be specified through command line parameters.

- `DNS: daze ... -dns 1.1.1.1:53`
- `DoT: daze ... -dns 1.1.1.1:853`
- `DoH: daze ... -dns https://1.1.1.1/dns-query`

This [article](https://www.cloudflare.com/learning/dns/dns-over-tls/) briefly describes the difference between them.

## Configuration: Protocols

Daze currently has 5 protocols.

**Ashe**

The default protocol used by daze is called ashe. Ashe is a TCP-based cryptographic proxy protocol designed to bypass firewalls while providing a good user experience.

Please note that it is the user's responsibility to ensure that the date and time on both the server and client are consistent. The ashe protocol allows for a deviation of up to two minutes.

**Baboon**

Protocol baboon is a variant of the ashe protocol that operates over HTTP. In this protocol, the daze server masquerades as an HTTP service and requires the user to provide the correct password in order to gain access to the proxy service. If the password is not provided, the daze server will behave as a normal HTTP service. To use the baboon protocol, you must specify the protocol name and a fake site:

```sh
$ daze server ... -p baboon -e https://github.com
$ daze client ... -p baboon
```

**Czar**

Protocol czar is an implementation of the ashe protocol based on TCP multiplexing. Multiplexing involves reusing a single TCP connection for multiple ashe protocols, which saves time on the TCP three-way handshake. However, this may result in a slight decrease in data transfer rate (approximately 0.19%). In most cases, using Protocol czar provides a better user experience compared to using the ashe protocol directly.

```sh
$ daze server ... -p czar
$ daze client ... -p czar
```

**Dahlia**

Dahlia is a protocol used for encrypted port forwarding. Unlike many common port forwarding tools, it requires both a server and a client to be configured. Communication between the server and client is encrypted in order to bypass detection by firewalls.

```sh
# Port forwarding from 20002 to 20000:
$ daze server -l :20001 -e 127.0.0.1:20000 -p dahlia
$ daze client -l :20002 -s 127.0.0.1:20001 -p dahlia
```

Reminder again: Dahlia is not a proxy protocol but a port forwarding protocol.


**Etch**

Protocol etch is the ashe protocol carried over QUIC. QUIC is a UDP-based, multiplexed, encrypted transport, so etch enjoys two practical advantages over plain ashe: a single UDP flow is much harder for middleboxes to throttle than a long-lived TCP connection, and the connection survives transient packet loss and network address changes that would tear down TCP. Because the ashe layer already encrypts the traffic with a pre-shared key, the TLS 1.3 handshake required by QUIC uses a self-signed certificate generated on the fly and the client skips verification; trust is entirely established by the ashe password.

```sh
$ daze server ... -p etch
$ daze client ... -p etch
```

## Configuration: Proxy Control

Proxy control is a rule that determines whether network requests (TCP and UDP) go directly to the destination or are forwarded to the daze server. Use the `-f` option in the daze client to adjust the proxy configuration.

- Use local network for all requests.
- Use remote server for all requests.
- Use both local and remote server (default). In this case, the following two configuration files are enabled:

**rule.ls**

Daze uses a "rule.ls" file to customize your own rules. File "rule.ls" has the highest priority in routers so you should carefully maintain it. The "rule.ls" is located on the "rule.ls" by default, or you can use `daze client -r path/to/rule.ls` to apply it. Its syntax is very simple:

```text
L a.com
R b.com
B c.com
```

- L(ocale) means using local network.
- R(emote) means using proxy.
- B(anned) means to block it, often used to block ads.

Glob is supported, such as `R *.google.com`.

**rule.cidr**

Daze also uses a CIDR(Classless Inter-Domain Routing) file to route addresses. The CIDR file is located at "rule.cidr", and has a lower priority than "rule.ls".

By default, daze has configured rule.cidr for China's mainland. You can update it manually via `daze gen cn`, this will pull the latest data from [http://ftp.apnic.net/apnic/stats/apnic/delegated-apnic-latest](http://ftp.apnic.net/apnic/stats/apnic/delegated-apnic-latest).

## License

MIT.
