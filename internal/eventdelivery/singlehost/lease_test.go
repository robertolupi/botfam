package singlehost_test

import (
	"context"
	"io/ioutil"
	"os"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/pelletier/go-toml/v2"
	pb "github.com/robertolupi/botfam/internal/eventdelivery/contract/botfam/eventdelivery/v2"
	"github.com/robertolupi/botfam/internal/eventdelivery/singlehost"
	"google.golang.org/protobuf/types/known/durationpb"
)

func TestLeaseAcquireExclusivityAndRelease(t *testing.T) {
	// Setup user home directory redirect to a temp dir so we don't pollute real ~/.botfam
	tmpHome, err := ioutil.TempDir("", "botfam-home-*")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(tmpHome)

	oldHome := os.Getenv("HOME")
	os.Setenv("HOME", tmpHome)
	defer os.Setenv("HOME", oldHome)

	ctx := context.Background()
	scope := &pb.Scope{RepoOwner: "botfam", RepoName: "test-repo"}

	l1 := singlehost.NewLease()
	defer l1.Close()

	// Acquire first lease
	req1 := connect.NewRequest(&pb.AcquireRequest{
		Scope:          scope,
		HolderIdentity: "holder-1",
		Ttl:            durationpb.New(5 * time.Second),
	})
	grant1, err := l1.Acquire(ctx, req1)
	if err != nil {
		t.Fatalf("l1.Acquire: %v", err)
	}
	if !grant1.Msg.GetGranted() {
		t.Fatal("expected l1 lease to be granted")
	}
	if grant1.Msg.GetFencingToken() != 1 {
		t.Errorf("expected fencing token 1, got %d", grant1.Msg.GetFencingToken())
	}

	// Try acquiring second lease using another descriptor/struct
	l2 := singlehost.NewLease()
	defer l2.Close()

	req2 := connect.NewRequest(&pb.AcquireRequest{
		Scope:          scope,
		HolderIdentity: "holder-2",
		Ttl:            durationpb.New(5 * time.Second),
	})
	grant2, err := l2.Acquire(ctx, req2)
	if err != nil {
		t.Fatalf("l2.Acquire: %v", err)
	}
	if grant2.Msg.GetGranted() {
		t.Fatal("expected l2 lease to be busy/rejected")
	}

	// Resolve resolver
	resolver := singlehost.NewSessionResolver()
	res, err := resolver.Resolve(ctx, connect.NewRequest(scope))
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if !res.Msg.GetFound() {
		t.Fatal("expected resolver to find active lease")
	}
	if res.Msg.GetSessionId() != grant1.Msg.GetLeaseId() {
		t.Errorf("resolver mismatch session ID: got %s, want %s", res.Msg.GetSessionId(), grant1.Msg.GetLeaseId())
	}

	// Renew
	renewRes, err := l1.Renew(ctx, connect.NewRequest(&pb.RenewRequest{LeaseId: grant1.Msg.GetLeaseId()}))
	if err != nil {
		t.Fatalf("Renew: %v", err)
	}
	if !renewRes.Msg.GetGranted() {
		t.Fatal("expected renew to succeed")
	}

	// Simulate a crash: close the lease (releases flock but leaves file on disk)
	if err := l1.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// To simulate the process dying, we overwrite the file with a dead PID
	// but keeping the fencing token so it can be incremented next time.
	deadPID := getDeadPID(t)
	path, err := singlehost.ConfigPath(scope.GetRepoName())
	if err != nil {
		t.Fatal(err)
	}
	sfBytes, err := ioutil.ReadFile(path)
	if err == nil {
		var sf singlehost.SessionFile
		if toml.Unmarshal(sfBytes, &sf) == nil {
			sf.PID = deadPID
			newData, err := toml.Marshal(sf)
			if err == nil {
				_ = ioutil.WriteFile(path, newData, 0o644)
			}
		}
	}

	// Check resolver now reports not found (because PID is dead)
	resAfterCrash, err := resolver.Resolve(ctx, connect.NewRequest(scope))
	if err != nil {
		t.Fatalf("Resolve after crash: %v", err)
	}
	if resAfterCrash.Msg.GetFound() {
		t.Fatal("expected resolver to not find crashed lease")
	}

	// Acquire lease again — fencing token should increment
	grant1Retry, err := l1.Acquire(ctx, req1)
	if err != nil {
		t.Fatalf("l1.Acquire retry: %v", err)
	}
	if !grant1Retry.Msg.GetGranted() {
		t.Fatal("expected l1 lease retry to be granted")
	}
	if grant1Retry.Msg.GetFencingToken() != 2 {
		t.Errorf("expected incremented fencing token 2, got %d", grant1Retry.Msg.GetFencingToken())
	}
}

func getDeadPID(t *testing.T) int {
	t.Helper()
	return getDeadPIDViaExec(t)
}

func getDeadPIDViaExec(t *testing.T) int {
	t.Helper()
	var procAttr os.ProcAttr
	procAttr.Files = []*os.File{nil, nil, nil}
	p, err := os.StartProcess("/usr/bin/true", []string{"true"}, &procAttr)
	if err != nil {
		// fallback for non-standard path
		p, err = os.StartProcess("/bin/true", []string{"true"}, &procAttr)
		if err != nil {
			t.Fatal(err)
		}
	}
	pid := p.Pid
	_, _ = p.Wait()
	return pid
}
