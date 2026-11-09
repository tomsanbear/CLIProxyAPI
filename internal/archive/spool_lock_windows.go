//go:build windows

package archive

import (
	"errors"
	"os"
)

// lockSpool is unsupported on Windows: the spool contract relies on flock and
// on unlinking files a reader may hold open (spec sections 1 and 3).
func lockSpool(string) (*os.File, error) {
	return nil, errors.New("archive: the spool is not supported on windows")
}
