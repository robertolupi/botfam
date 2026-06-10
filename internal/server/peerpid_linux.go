//go:build linux

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
	ucred, err := syscall.GetsockoptUcred(fd, syscall.SOL_SOCKET, syscall.SO_PEERCRED)
	if err != nil {
		return 0, err
	}
	return int(ucred.Pid), nil
}
