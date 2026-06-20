package singlehost

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"syscall"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	pb "github.com/robertolupi/botfam/internal/eventdelivery/contract/botfam/eventdelivery/v2"
	"github.com/robertolupi/botfam/internal/famconfig"
	"github.com/pelletier/go-toml/v2"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Lease implements pb.Lease via connect.
type Lease struct {
	mu           sync.Mutex
	file         *os.File
	leaseID      string
	fencingToken uint64
	repoName     string
}

// NewLease creates a new single-host Lease handler.
func NewLease() *Lease {
	return &Lease{}
}

// Close closes any open lease file descriptor, releasing the flock.
func (l *Lease) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.file != nil {
		_ = syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
		err := l.file.Close()
		l.file = nil
		l.leaseID = ""
		l.fencingToken = 0
		return err
	}
	return nil
}

// Acquire attempts to acquire an exclusive flock lease over the repository.
func (l *Lease) Acquire(ctx context.Context, req *connect.Request[pb.AcquireRequest]) (*connect.Response[pb.Grant], error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	scope := req.Msg.GetScope()
	repoName := scope.GetRepoName()
	if repoName == "" {
		if wd, err := os.Getwd(); err == nil {
			repoName = famconfig.ResolveRepoName(wd)
		}
	}
	if repoName == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("cannot resolve repository name for lease scope"))
	}

	path, err := ConfigPath(repoName)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	// 1. Open the file
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("open session file: %w", err))
	}

	// 2. Try to acquire flock
	err = syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if err != nil {
		_ = f.Close()
		if err == syscall.EWOULDBLOCK {
			// Lock is held by another process. Read the file to see if the holder is live.
			data, readErr := os.ReadFile(path)
			if readErr == nil {
				var sf SessionFile
				if toml.Unmarshal(data, &sf) == nil {
					if isProcessLive(sf.PID) {
						// Holder is live, return busy
						return connect.NewResponse(&pb.Grant{Granted: false}), nil
					}
				}
			}
			// If we got here, flock failed but the process isn't live? That shouldn't happen.
			return connect.NewResponse(&pb.Grant{Granted: false}), nil
		}
		return nil, connect.NewError(connect.CodeInternal, fmt.Errorf("flock failed: %w", err))
	}

	// 3. We have the lock! If there is an existing file, read the fencing token to increment it.
	var sf SessionFile
	decoder := toml.NewDecoder(f)
	var fencingToken uint64 = 1
	if decoder.Decode(&sf) == nil {
		if sf.FencingToken > 0 {
			fencingToken = sf.FencingToken + 1
		}
	}

	// Generate lease ID and construct the new config
	leaseID := uuid.New().String()
	sf = SessionFile{
		LeaseID:      leaseID,
		FencingToken: fencingToken,
		PID:          os.Getpid(),
		Addr:         "127.0.0.1:0", // Default placeholder for single-host local endpoint
		Token:        uuid.New().String(),
	}

	// Write back the new configuration details
	if _, err := f.Seek(0, 0); err != nil {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if err := f.Truncate(0); err != nil {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	encoder := toml.NewEncoder(f)
	if err := encoder.Encode(sf); err != nil {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	if err := f.Sync(); err != nil {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
		return nil, connect.NewError(connect.CodeInternal, err)
	}

	// Close previous lease if we held one
	if l.file != nil {
		_ = syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
		_ = l.file.Close()
	}

	l.file = f
	l.leaseID = leaseID
	l.fencingToken = fencingToken
	l.repoName = repoName

	ttl := 10 * time.Second
	if req.Msg.GetTtl() != nil {
		ttl = req.Msg.GetTtl().AsDuration()
	}

	return connect.NewResponse(&pb.Grant{
		Granted:      true,
		LeaseId:      leaseID,
		FencingToken: fencingToken,
		ExpiresAt:    timestamppb.New(time.Now().Add(ttl)),
	}), nil
}

// Renew extends the TTL of the currently held lease.
func (l *Lease) Renew(ctx context.Context, req *connect.Request[pb.RenewRequest]) (*connect.Response[pb.Grant], error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	reqLeaseID := req.Msg.GetLeaseId()
	if l.file == nil || l.leaseID == "" || l.leaseID != reqLeaseID {
		return connect.NewResponse(&pb.Grant{Granted: false}), nil
	}

	// Update the file contents or just extend TTL. For singlehost flock, renewing is cheap.
	ttl := 10 * time.Second
	return connect.NewResponse(&pb.Grant{
		Granted:      true,
		LeaseId:      l.leaseID,
		FencingToken: l.fencingToken,
		ExpiresAt:    timestamppb.New(time.Now().Add(ttl)),
	}), nil
}

// Release releases the lease, unlocks the flock, and removes the session file.
func (l *Lease) Release(ctx context.Context, req *connect.Request[pb.ReleaseRequest]) (*connect.Response[emptypb.Empty], error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	reqLeaseID := req.Msg.GetLeaseId()
	if l.file == nil || l.leaseID == "" || l.leaseID != reqLeaseID {
		return connect.NewResponse(&emptypb.Empty{}), nil
	}

	_ = syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN)
	_ = l.file.Close()
	l.file = nil

	if l.repoName != "" {
		if path, err := ConfigPath(l.repoName); err == nil {
			_ = os.Remove(path)
		}
	}

	l.leaseID = ""
	l.fencingToken = 0
	l.repoName = ""

	return connect.NewResponse(&emptypb.Empty{}), nil
}
