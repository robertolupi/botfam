package seam_test

import (
	"context"
	"testing"

	"github.com/robertolupi/botfam/internal/famctx"
)

func TestMustHaveIdentity_panicsWithoutStamp(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatal("expected panic when context has no famctx stamp, got none")
		}
	}()
	famctx.MustHaveIdentity(context.Background())
}

func TestMustHaveIdentity_returnsWithStamp(t *testing.T) {
	ctx := famctx.NewContext(context.Background(), famctx.Context{})
	_ = famctx.MustHaveIdentity(ctx) // must not panic
}
