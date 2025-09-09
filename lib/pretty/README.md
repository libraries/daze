# Pretty

Package pretty provides utilities for beautifying console output.

**Progress**

```sh
$ go run cmd/progress/main.go

2025/03/12 09:53:42 pretty: [=========================>                   ]  59%
```

**Table**

```sh
$ go run cmd/table/main.go

2025/09/09 10:34:44 pretty: City name Area Population Annual Rainfall
2025/09/09 10:34:44 pretty: -----------------------------------------
2025/09/09 10:34:44 pretty: Adelaide  1295    1158259           600.5
2025/09/09 10:34:44 pretty: Brisbane  5905    1857594          1146.4
2025/09/09 10:34:44 pretty: Darwin     112     120900          1714.7
2025/09/09 10:34:44 pretty: Hobart    1357     205556           619.5
2025/09/09 10:34:44 pretty: Melbourne 1566    3806092           646.9
2025/09/09 10:34:44 pretty: Perth     5386    1554769           869.4
2025/09/09 10:34:44 pretty: Sydney    2058    4336374          1214.8
```

**Tree**

```sh
$ go run cmd/tree/main.go

2025/09/08 16:39:26 pretty: .
2025/09/08 16:39:26 pretty: ├── README.md
2025/09/08 16:39:26 pretty: ├── cmd
2025/09/08 16:39:26 pretty: │   ├── progress
2025/09/08 16:39:26 pretty: │   │   └── main.go
2025/09/08 16:39:26 pretty: │   ├── table
2025/09/08 16:39:26 pretty: │   │   └── main.go
2025/09/08 16:39:26 pretty: │   └── tree
2025/09/08 16:39:26 pretty: │       └── main.go
2025/09/08 16:39:26 pretty: └── pretty.go
```
