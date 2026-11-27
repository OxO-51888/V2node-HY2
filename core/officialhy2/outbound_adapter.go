package officialhy2

import (
	"fmt"
	"net"
	"strconv"
	"sync/atomic"

	"github.com/apernet/hysteria/core/v2/server"
	"github.com/apernet/hysteria/extras/v2/outbounds"
)

type fastOutboundAdapter struct {
	outbound outbounds.PluggableOutbound
}

type fastUDPConnAdapter struct {
	conn          outbounds.UDPConn
	lastWriteAddr atomic.Value
}

type cachedAddrEx struct {
	raw string
	ex  *outbounds.AddrEx
}

func (a *fastOutboundAdapter) TCP(info *server.RequestInfo, reqAddr string) (net.Conn, error) {
	addr, err := parseAddrEx(reqAddr)
	if err != nil {
		return nil, err
	}
	addr.Info = info
	return a.outbound.TCP(addr)
}

func (a *fastOutboundAdapter) UDP(info *server.RequestInfo, reqAddr string) (server.UDPConn, error) {
	addr, err := parseAddrEx(reqAddr)
	if err != nil {
		return nil, err
	}
	addr.Info = info
	conn, err := a.outbound.UDP(addr)
	if err != nil {
		return nil, err
	}
	return &fastUDPConnAdapter{conn: conn}, nil
}

func (a *fastOutboundAdapter) CheckUDP(info *server.RequestInfo, reqAddr string) error {
	addr, err := parseAddrEx(reqAddr)
	if err != nil {
		return err
	}
	addr.Info = info
	return a.outbound.CheckUDP(addr)
}

func (u *fastUDPConnAdapter) ReadFrom(b []byte) (int, string, error) {
	n, addr, err := u.conn.ReadFrom(b)
	if addr == nil {
		return n, "", err
	}
	return n, addr.String(), err
}

func (u *fastUDPConnAdapter) WriteTo(b []byte, addr string) (int, error) {
	if cached, ok := u.lastWriteAddr.Load().(cachedAddrEx); ok && cached.raw == addr {
		return u.conn.WriteTo(b, cached.ex)
	}
	addrEx, err := parseAddrEx(addr)
	if err != nil {
		return 0, err
	}
	u.lastWriteAddr.Store(cachedAddrEx{raw: addr, ex: addrEx})
	return u.conn.WriteTo(b, addrEx)
}

func (u *fastUDPConnAdapter) Close() error {
	return u.conn.Close()
}

func parseAddrEx(addr string) (*outbounds.AddrEx, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	portUint, err := strconv.ParseUint(port, 10, 16)
	if err != nil {
		return nil, fmt.Errorf("invalid port: %w", err)
	}
	return &outbounds.AddrEx{
		Host: host,
		Port: uint16(portUint),
	}, nil
}
