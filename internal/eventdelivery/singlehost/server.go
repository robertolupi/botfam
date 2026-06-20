package singlehost

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
)

type Server struct {
	HTTPServer *http.Server
	listener   net.Listener
}

func Serve(ctx context.Context, socketPath string, handler http.Handler) (*Server, error) {
	if err := os.Remove(socketPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		return nil, err
	}
	server := &Server{
		HTTPServer: &http.Server{Handler: handler},
		listener:   listener,
	}
	go func() {
		<-ctx.Done()
		_ = server.HTTPServer.Shutdown(context.Background())
	}()
	go func() {
		if err := server.HTTPServer.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			_ = listener.Close()
		}
	}()
	return server, nil
}

func (s *Server) Addr() string {
	if s == nil || s.listener == nil {
		return ""
	}
	return s.listener.Addr().String()
}
