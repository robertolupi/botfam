package store

import (
	"fmt"
	"regexp"
)

var nameRE = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func ValidateName(kind, name string) error {
	if !nameRE.MatchString(name) {
		return fmt.Errorf("invalid %s %q: must match [A-Za-z0-9_-]+", kind, name)
	}
	return nil
}
