package tinyfish

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"time"
)

func newDefaultHTTPClient(timeout time.Duration) *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = safeDialContext
	return &http.Client{
		Timeout:   timeout,
		Transport: transport,
	}
}

func safeDialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("host resolved no addresses")
	}

	dialer := &net.Dialer{}
	var lastErr error
	for _, ip := range ips {
		addr, ok := netip.AddrFromSlice(ip.IP)
		if !ok || privateAddr(addr) {
			return nil, fmt.Errorf("resolved private address blocked")
		}
		conn, err := dialer.DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port))
		if err == nil {
			return conn, nil
		}
		lastErr = err
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("host resolved no dialable addresses")
}

func privateAddr(addr netip.Addr) bool {
	return addr.IsLoopback() ||
		addr.IsPrivate() ||
		addr.IsLinkLocalUnicast() ||
		addr.IsLinkLocalMulticast() ||
		addr.IsUnspecified()
}
