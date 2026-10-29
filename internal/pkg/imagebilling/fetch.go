package imagebilling

import (
	"context"
	"fmt"
	"image"
	"io"
	"net"
	"net/http"
	"net/netip"
	"time"
)

// A dedicated unauthenticated transport checks the actual dial address, including
// redirect targets, so provider URLs cannot access local services or credentials.
var imageClient = &http.Client{
	Timeout: 3 * time.Second,
	Transport: &http.Transport{
		DisableKeepAlives: true,
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			host, port, err := net.SplitHostPort(address)
			if err != nil {
				return nil, err
			}
			ips, err := net.DefaultResolver.LookupNetIP(ctx, "ip", host)
			if err != nil {
				return nil, err
			}
			for _, ip := range ips {
				if !publicAddress(ip.Unmap()) {
					return nil, fmt.Errorf("image address is not public")
				}
			}
			if len(ips) == 0 {
				return nil, fmt.Errorf("image address is empty")
			}
			return (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ips[0].String(), port))
		},
	},
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 3 {
			return fmt.Errorf("too many image redirects")
		}
		if req.URL.User != nil || (req.URL.Scheme != "http" && req.URL.Scheme != "https") {
			return fmt.Errorf("unsupported image URL")
		}
		return nil
	},
}

func publicAddress(ip netip.Addr) bool {
	if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
		return false
	}
	for _, block := range []string{"100.64.0.0/10", "192.0.0.0/24", "192.0.2.0/24", "198.18.0.0/15", "198.51.100.0/24", "203.0.113.0/24", "240.0.0.0/4", "2001:db8::/32", "64:ff9b::/96"} {
		if netip.MustParsePrefix(block).Contains(ip) {
			return false
		}
	}
	return true
}

func Fetch(ctx context.Context, url string) (image.Config, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return image.Config{}, err
	}
	if req.URL.User != nil || (req.URL.Scheme != "http" && req.URL.Scheme != "https") {
		return image.Config{}, fmt.Errorf("unsupported image URL")
	}
	response, err := imageClient.Do(req)
	if err != nil {
		return image.Config{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK && response.StatusCode != http.StatusPartialContent {
		return image.Config{}, fmt.Errorf("image HTTP status %d", response.StatusCode)
	}
	config, _, err := image.DecodeConfig(io.LimitReader(response.Body, 1<<20))
	return config, err
}
