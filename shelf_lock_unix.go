// SPDX-License-Identifier: Apache-2.0

//go:build unix

package evidencebook

import (
	"errors"
	"os"
	"syscall"
)

func init() { lockWriter = flockWriter }

// flockWriter takes an exclusive advisory lock on path, waiting for any other
// holder, as capsule-cli's deal store lock does (lockDealStore), so a shelf
// and a writer of the same store never run at once.
func flockWriter(path string) (func() error, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		return nil, errors.Join(err, f.Close())
	}
	return func() error {
		return errors.Join(syscall.Flock(int(f.Fd()), syscall.LOCK_UN), f.Close())
	}, nil
}
