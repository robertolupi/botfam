// Package seam provides the CattleSeam interceptor layer: thin proxy types that
// enforce famctx.MustHaveIdentity at every service-interface entry point, and
// (in test mode) act as a chaos valve.
//
// Proxies are added here as real service interfaces are defined in their owning
// leaf packages. Each proxy follows the same pattern:
//
//	type seamFoo struct{ inner SomeInterface }
//
//	func Foo(inner SomeInterface) SomeInterface { return seamFoo{inner} }
//
//	func (s seamFoo) Method(ctx context.Context, …) (…, error) {
//	    guard(ctx)
//	    return s.inner.Method(ctx, …)
//	}
//
// Dependency position: seam imports famctx and the owning leaf package for each
// interface it wraps. It never imports cli or mcp.
package seam

import (
	"context"

	"github.com/robertolupi/botfam/internal/famctx"
)

// guard enforces that ctx carries a stamped famctx identity.
// All proxy entry points call this before delegating to the inner implementation.
func guard(ctx context.Context) famctx.Context {
	return famctx.MustHaveIdentity(ctx)
}
