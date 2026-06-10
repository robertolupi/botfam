//go:build darwin

package server

import (
	"net"
	"syscall"
)

func getPeerPID(conn *net.UnixConn) (int, error) {
	file, err := conn.File()
	if err != nil {
		return 0, err
	}
	defer file.Close()
	fd := int(file.Fd())
	// SOL_LOCAL is 0 on Darwin, LOCAL_PEERPID is 0x002
	return syscall.GetsockoptInt(fd, 0, 0x002)
}
