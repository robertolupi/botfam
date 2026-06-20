package singlehost

import (
	"context"
	"net"
	"net/http"
	"time"
)

func UnixHTTPClient(socketPath string) *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				var dialer net.Dialer
				return dialer.DialContext(ctx, "unix", socketPath)
			},
		},
		Timeout: 30 * time.Second,
	}
}
