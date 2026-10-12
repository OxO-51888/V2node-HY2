# V2node-HY2

HY2-only V2Board backend built around the official Hysteria2 server core.

This branch removes the Xray-core runtime path and only accepts `hysteria2` nodes.

## UDP Receive Buffer

Each HY2 listener requests a 32 MiB socket receive buffer before QUIC starts.
On Linux, `SO_RCVBUF` reports 64 MiB including kernel accounting overhead.
The backend verifies the effective size and, if permitted, uses a socket-local
`SO_RCVBUFFORCE` fallback without modifying system-wide network settings.
Insufficient permissions produce a warning, not a startup failure. Larger
existing buffers are preserved. This is separate from QUIC stream/connection
receive windows and does not change congestion control, bandwidth, or blocking rules.

## Stability

- User removal revokes cached traffic authorization for existing TCP and UDP sessions.
- Online-device snapshots and user updates are synchronized; traffic callbacks use atomic flags.
- Listener recreation keeps retrying with bounded backoff and releases failed listeners.
- An explicit empty user array removes previous users. Unchanged or invalid responses do not.

## Install

```bash
bash <(curl -Ls https://raw.githubusercontent.com/OxO-51888/V2node-HY2/main/script/install.sh)
```

## Build

```bash
GOEXPERIMENT=jsonv2 go build -trimpath -ldflags "-s -w -X github.com/OxO-51888/V2node-HY2/cmd.version=local" -o v2node .
```
