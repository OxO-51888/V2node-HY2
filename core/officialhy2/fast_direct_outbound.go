package officialhy2

import (
	"fmt"
	"net"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/apernet/hysteria/extras/v2/outbounds"
)

const outboundDialTimeout = 10 * time.Second

type fastDirectOutbound struct {
	dialer net.Dialer
}

type fastDirectUDPConn struct {
	*net.UDPConn
	lastWriteAddr atomic.Value
}

type cachedUDPAddrEx struct {
	raw string
	udp *net.UDPAddr
}

func newFastDirectOutbound() outbounds.PluggableOutbound {
	return &fastDirectOutbound{
		dialer: net.Dialer{Timeout: outboundDialTimeout},
	}
}

func (d *fastDirectOutbound) TCP(reqAddr *outbounds.AddrEx) (net.Conn, error) {
	address := reqAddr.String()
	if tcpAddr, err := resolveAddrExTCP(reqAddr); err == nil {
		address = tcpAddr
	}
	return d.dialer.Dial("tcp4", address)
}

func (d *fastDirectOutbound) UDP(_ *outbounds.AddrEx) (outbounds.UDPConn, error) {
	conn, err := net.ListenUDP("udp", nil)
	if err != nil {
		return nil, err
	}
	return &fastDirectUDPConn{UDPConn: conn}, nil
}

func (d *fastDirectOutbound) CheckUDP(_ *outbounds.AddrEx) error {
	return nil
}

func (c *fastDirectUDPConn) ReadFrom(b []byte) (int, *outbounds.AddrEx, error) {
	n, addr, err := c.UDPConn.ReadFromUDP(b)
	if addr == nil {
		return n, nil, err
	}
	return n, &outbounds.AddrEx{
		Host: addr.IP.String(),
		Port: uint16(addr.Port),
	}, err
}

func (c *fastDirectUDPConn) WriteTo(b []byte, addr *outbounds.AddrEx) (int, error) {
	raw := addr.String()
	if cached, ok := c.lastWriteAddr.Load().(cachedUDPAddrEx); ok && cached.raw == raw {
		return c.UDPConn.WriteToUDP(b, cached.udp)
	}
	udpAddr, err := resolveAddrExUDP(addr)
	if err != nil {
		return 0, err
	}
	c.lastWriteAddr.Store(cachedUDPAddrEx{raw: raw, udp: udpAddr})
	return c.UDPConn.WriteToUDP(b, udpAddr)
}

func resolveAddrExUDP(addr *outbounds.AddrEx) (*net.UDPAddr, error) {
	if addr.ResolveInfo != nil {
		if ip := preferredUDPIP(addr.ResolveInfo); ip != nil {
			return &net.UDPAddr{IP: ip, Port: int(addr.Port)}, nil
		}
		if addr.ResolveInfo.Err != nil {
			return nil, fmt.Errorf("resolve error: %w", addr.ResolveInfo.Err)
		}
		return nil, fmt.Errorf("no address available for %s", net.JoinHostPort(addr.Host, strconv.Itoa(int(addr.Port))))
	}
	if ip := net.ParseIP(addr.Host); ip != nil {
		return &net.UDPAddr{IP: ip, Port: int(addr.Port)}, nil
	}
	ips, err := net.LookupIP(addr.Host)
	if err != nil {
		return nil, err
	}
	for _, ip := range ips {
		if ip.To4() != nil {
			return &net.UDPAddr{IP: ip, Port: int(addr.Port)}, nil
		}
	}
	for _, ip := range ips {
		if ip.To16() != nil {
			return &net.UDPAddr{IP: ip, Port: int(addr.Port)}, nil
		}
	}
	return nil, fmt.Errorf("no address available for %s", net.JoinHostPort(addr.Host, strconv.Itoa(int(addr.Port))))
}

func resolveAddrExTCP(addr *outbounds.AddrEx) (string, error) {
	port := strconv.Itoa(int(addr.Port))
	if addr.ResolveInfo != nil {
		if addr.ResolveInfo.IPv4 != nil {
			return net.JoinHostPort(addr.ResolveInfo.IPv4.String(), port), nil
		}
		if addr.ResolveInfo.Err != nil {
			return "", fmt.Errorf("resolve error: %w", addr.ResolveInfo.Err)
		}
		return "", fmt.Errorf("no IPv4 address available for %s", net.JoinHostPort(addr.Host, port))
	}
	if ip := net.ParseIP(addr.Host); ip != nil {
		if ip.To4() == nil {
			return "", fmt.Errorf("IPv6 literal is not allowed for forced IPv4 outbound: %s", addr.Host)
		}
		return net.JoinHostPort(ip.String(), port), nil
	}
	ips, err := net.LookupIP(addr.Host)
	if err != nil {
		return "", err
	}
	for _, ip := range ips {
		if ip.To4() != nil {
			return net.JoinHostPort(ip.String(), port), nil
		}
	}
	return "", fmt.Errorf("no IPv4 address available for %s", net.JoinHostPort(addr.Host, port))
}

func preferredUDPIP(info *outbounds.ResolveInfo) net.IP {
	if info.IPv4 != nil {
		return info.IPv4
	}
	return info.IPv6
}
