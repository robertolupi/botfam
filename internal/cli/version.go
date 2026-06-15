package cli

import "github.com/robertolupi/botfam/internal/version"

// Version metadata moved to the internal/version leaf (#314). Re-exported so
// the (soon-relocated) version command compiles unchanged.
func GetVersion() string { return version.GetVersion() }
