//go:build !darwin && !linux

package server

import (
	"errors"
	"net"
)

func getPeerPID(conn *net.UnixConn) (int, error) {
	return 0, errors.New("peer PID not supported on this platform")
}
