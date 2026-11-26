# V2node-HY2

HY2-only V2Board backend built around the official Hysteria2 server core.

This branch removes the Xray-core runtime path and only accepts `hysteria2` nodes.

## Panel Compatibility

This version requires the dedicated `hy2-1` panel API and its database migration.
It does not fall back to UniProxy or the generic V2 server API. Upgrade the panel
and backend as one coordinated release; do not install this candidate against an
unmodified panel. See [the API contract](docs/panel-contract.md).

## Server Bandwidth Policy

`V2NODE_FORCE_SERVER_BANDWIDTH` controls the HY2 server bandwidth policy for
all listeners in one process. Unset or `true` forces the server bandwidth policy.
Set `false` to use normal HY2 negotiation with the client's bandwidth settings.
Neither setting changes panel rates, user quotas, or blocking rules. Set the
environment variable on the service and restart it to apply the change. Remove
the override and restart to restore the default. On systemd the installer
supplies a vendor default; an existing administrator override such as
`70-hy2-force-bandwidth.conf` set to `false` stays in effect after reinstall.

## Static Site Pages

Place the site's public `index.html` and referenced static assets under
`/etc/v2node/masq/<site>/`. Replacing a file atomically is sufficient; no
service restart is needed to refresh a page. The handler serves GET and HEAD
for public web file types only. It does not list directories or serve files
outside the site's root. A missing or empty homepage returns 503 instead of
a successful blank page. No publication URLs are bundled in this repository.

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
