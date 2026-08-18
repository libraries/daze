# Daze

![img](./res/daze.jpg)

[English](./README.md) | 中文

Daze 是一款帮助你穿越防火墙的软件, 换句话说, 它是一个代理工具. 它使用简单而高效的协议, 确保你不会被检测或屏蔽.

## 简介

Daze 被设计为一个单文件应用. 首先, 编译或[下载](https://github.com/libraries/daze) Daze:

```sh
$ git clone https://github.com/libraries/daze
$ cd daze

# Linux 或 macOS
$ cmd/develop.sh
# Windows
$ cmd/develop.ps1
```

编译结果会保存到 `bin` 目录中. 你可以只保留这个目录, 其他文件都不再需要.

Daze 的使用非常简单:

```sh
# 服务端
# 你需要一台可以访问互联网的机器, 然后执行以下命令:
$ daze server -l 0.0.0.0:1081 -k $PASSWORD

# 客户端
# 使用以下命令连接到你的服务器(将 $SERVER 替换为服务器 IP):
$ daze client -s $SERVER:1081 -k $PASSWORD
# 现在你可以自由访问互联网了
$ curl -x http://127.0.0.1:1080    http://google.com
$ curl -x http://127.0.0.1:1080    https://google.com
$ curl -x socks4://127.0.0.1:1080  https://google.com
$ curl -x socks4a://127.0.0.1:1080 https://google.com
$ curl -x socks5://127.0.0.1:1080  https://google.com
$ curl -x socks5h://127.0.0.1:1080 https://google.com
```

Daze 仍在开发中. 请确保服务端和客户端使用相同的版本号(可通过 `daze -v` 命令查看)或相同的 commit hash.

## 部署

Daze 完全使用 Go 语言实现, 因此几乎可以运行在任何操作系统上. 以下是我经常使用的一些浏览器和操作系统:

0. Android. 交叉编译 Android 版本的 Daze: `GOOS=android GOARCH=arm64 go build -o daze github.com/libraries/daze/cmd/daze`. 将编译后的文件推送到手机上. 你可以使用 [adb](https://developer.android.com/studio/command-line/adb), 也可以创建一个 HTTP 服务器, 然后在 [termux](https://play.google.com/store/apps/details?id=com.termux&hl=en) 中使用 `wget` 下载 Daze. 在 Termux 中运行 `daze client -l 127.0.0.1:1080 ...`. 设置手机代理: WLAN -> 设置 -> 代理 -> 填写 `127.0.0.1:1080`
0. Chrome. Chrome 不支持直接设置代理, 因此必须使用第三方插件. [Proxy SwitchyOmega](https://chrome.google.com/webstore/detail/proxy-switchyomega/padekgcemlokbadohgkifijomclgjgif?hl=en) 的效果很好.
0. Firefox 可以在 `连接设置` -> `手动代理配置` 中配置代理, 将 `SOCKSv5 Host` 设置为 `127.0.0.1`, 端口设置为 `1080`. 如果页面上有 `使用远程 DNS` 选项, 请勾选它.

## 配置: 带宽限制

你可以限制 Daze 使用的最大带宽. 通常来说, 对于 Daze 服务端, 建议将带宽设置为略小于物理带宽的值.

```sh
# 对于 Daze 服务端, 如果物理带宽为 3M, 可以设置 -b 320k, 其中 320 = 3 * 1024 / 8 - 64.
$ daze server ... -b 320k
# 对于 Daze 客户端, 大多数情况下不需要进行配置.
$ daze client ...
```

## 配置: DNS

可以通过命令行参数指定 Daze 使用的 DNS 服务器和 DNS 协议.

- `DNS: daze ... -dns 1.1.1.1:53`
- `DoT: daze ... -dns 1.1.1.1:853`
- `DoH: daze ... -dns https://1.1.1.1/dns-query`

这篇[文章](https://www.cloudflare.com/learning/dns/dns-over-tls/)简要介绍了它们之间的区别.

## 配置: 协议

Daze 目前有 5 种协议.

**Ashe**

Daze 默认使用的协议叫作 Ashe. Ashe 是一种基于 TCP 的加密代理协议, 旨在绕过防火墙, 同时提供良好的用户体验.

请注意, 确保服务端和客户端的日期与时间一致是用户的责任. Ashe 协议允许的时间偏差最多为两分钟.

**Baboon**

Baboon 协议是 Ashe 协议的一个运行在 HTTP 之上的变体. 在该协议中, Daze 服务端会伪装成一个 HTTP 服务, 用户必须提供正确的密码才能访问代理服务. 如果没有提供密码, Daze 服务端的行为就会像普通的 HTTP 服务一样. 要使用 Baboon 协议, 必须指定协议名称和一个伪装网站:

```sh
$ daze server ... -p baboon -e https://github.com
$ daze client ... -p baboon
```

**Czar**

Czar 协议是基于 TCP 多路复用的 Ashe 协议实现. 多路复用会让多个 Ashe 协议复用同一个 TCP 连接, 从而节省 TCP 三次握手所需的时间. 不过, 这可能会导致数据传输速率略有下降(约 0.19%). 大多数情况下, 相比直接使用 Ashe 协议, 使用 Czar 协议可以提供更好的用户体验.

```sh
$ daze server ... -p czar
$ daze client ... -p czar
```

**Dahlia**

Dahlia 是一种用于加密端口转发的协议. 与许多常见的端口转发工具不同, 它要求同时配置服务端和客户端. 服务端与客户端之间的通信经过加密, 以绕过防火墙的检测.

```sh
# 将端口 20002 转发到 20000:
$ daze server -l :20001 -e 127.0.0.1:20000 -p dahlia
$ daze client -l :20002 -s 127.0.0.1:20001 -p dahlia
```

再次提醒: Dahlia 不是代理协议, 而是端口转发协议.

**Etch**

Etch 协议是通过 QUIC 传输的 Ashe 协议. QUIC 是一种基于 UDP, 支持多路复用且经过加密的传输协议, 因此相比普通 Ashe, Etch 具有两个实际优势: 中间设备更难限制单个 UDP 流的速度, 而不是限制一个长期存在的 TCP 连接; 此外, 连接还能在短暂的丢包和网络地址变化后继续保持, 而这些情况通常会导致 TCP 连接断开. 由于 Ashe 层已经使用预共享密钥对流量进行加密, QUIC 所需的 TLS 1.3 握手会使用运行时动态生成的自签名证书, 客户端则跳过证书验证; 信任完全由 Ashe 密码建立.

```sh
$ daze server ... -p etch
$ daze client ... -p etch
```

## 配置: 代理控制

代理控制是一组规则, 用于决定网络请求(TCP 和 UDP)是直接发送到目标地址, 还是转发到 Daze 服务端. 使用 Daze 客户端的 `-f` 选项可以调整代理配置.

- 所有请求都使用本地网络.
- 所有请求都使用远程服务器.
- 同时使用本地网络和远程服务器(默认). 在这种情况下, 以下两个配置文件会启用:

**rule.ls**

Daze 使用 `rule.ls` 文件来定制你自己的规则. `rule.ls` 在路由器中具有最高优先级, 因此请谨慎维护. 默认情况下, `rule.ls` 位于当前目录中; 也可以使用 `daze client -r path/to/rule.ls` 来应用指定的文件. 它的语法非常简单:

```text
L a.com
R b.com
B c.com
```

- L(Locale)表示使用本地网络.
- R(Remote)表示使用代理.
- B(Banned)表示阻止访问, 通常用于屏蔽广告.

支持 Glob, 例如 `R *.google.com`.

**rule.cidr**

Daze 还使用 CIDR(无类别域间路由)文件来路由地址. CIDR 文件位于 `rule.cidr`, 其优先级低于 `rule.ls`.

默认情况下, Daze 已经为中国大陆配置了 `rule.cidr`. 你可以通过 `daze cidr [regions]` 手动更新, 它会从 [http://ftp.apnic.net/apnic/stats/apnic/delegated-apnic-latest](http://ftp.apnic.net/apnic/stats/apnic/delegated-apnic-latest) 获取最新数据. 例如:

```sh
$ daze cidr cn # 中国
$ daze cidr hk # 中国香港特别行政区
$ daze cidr mo # 中国澳门特别行政区
```

所有受支持的地区列表见 [https://www.apnic.net/about-apnic/corporate-documents/documents/corporate/apnic-service-region/](https://www.apnic.net/about-apnic/corporate-documents/documents/corporate/apnic-service-region/).

## 许可证

MIT.
