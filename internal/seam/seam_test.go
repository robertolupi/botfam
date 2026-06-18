package seam_test

import (
	"context"
	"testing"

	"github.com/robertolupi/botfam/internal/famctx"
	"github.com/robertolupi/botfam/internal/proto"
	"github.com/robertolupi/botfam/internal/seam"
)

// stubBlackboard is a minimal Blackboard for testing the seam proxy.
type stubBlackboard struct {
	readCalled  bool
	writeCalled bool
}

func (s *stubBlackboard) ReadFact(_ context.Context, _ proto.Plane, _ string) (string, error) {
	s.readCalled = true
	return "ok", nil
}

func (s *stubBlackboard) WriteFact(_ context.Context, _ proto.Plane, _ string, _ string) error {
	s.writeCalled = true
	return nil
}

func stampedCtx() context.Context {
	return famctx.NewContext(context.Background(), famctx.Context{})
}

func TestMustHaveIdentity_panicsWithoutStamp(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic when context has no famctx stamp, got none")
		}
	}()
	famctx.MustHaveIdentity(context.Background())
}

func TestMustHaveIdentity_returnsWithStamp(t *testing.T) {
	ctx := stampedCtx()
	// must not panic
	_ = famctx.MustHaveIdentity(ctx)
}

func TestBlackboardProxy_enforcesStamp(t *testing.T) {
	stub := &stubBlackboard{}
	bb := seam.Blackboard(stub)

	// Unstamped context must panic.
	func() {
		defer func() {
			if r := recover(); r == nil {
				t.Error("ReadFact: expected panic on unstamped context")
			}
		}()
		_, _ = bb.ReadFact(context.Background(), proto.PlaneForge, "key")
	}()

	// Stamped context must delegate.
	ctx := stampedCtx()
	if _, err := bb.ReadFact(ctx, proto.PlaneForge, "key"); err != nil {
		t.Fatalf("ReadFact with stamped ctx: %v", err)
	}
	if !stub.readCalled {
		t.Error("ReadFact: inner not called")
	}

	if err := bb.WriteFact(ctx, proto.PlaneForge, "key", "val"); err != nil {
		t.Fatalf("WriteFact with stamped ctx: %v", err)
	}
	if !stub.writeCalled {
		t.Error("WriteFact: inner not called")
	}
}
