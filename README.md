# V2node-HY2

HY2-only V2Board backend built around the official Hysteria2 server core.

This branch removes the Xray-core runtime path and only accepts `hysteria2` nodes.

## Install

```bash
bash <(curl -Ls https://raw.githubusercontent.com/OxO-51888/V2node-HY2/main/script/install.sh)
```

## Build

```bash
GOEXPERIMENT=jsonv2 go build -trimpath -ldflags "-s -w -X github.com/OxO-51888/V2node-HY2/cmd.version=local" -o v2node .
```
