// SPDX-License-Identifier: Apache-2.0

//go:build unix

package evidencebook

import (
	"context"
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"
)

func init() { lockWriter = flockWriter }

// lockRetry is how often a waiting shelf tries the lock again.
const lockRetry = 50 * time.Millisecond

// flockWriter takes an exclusive advisory lock on path, as capsule-cli's deal
// store lock does (lockDealStore), so a shelf and a writer of the same store
// never run at once. It waits for any other holder until ctx is done, and
// then fails with ctx's error rather than hanging.
func flockWriter(ctx context.Context, path string) (func() error, error) {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return nil, err
	}
	for {
		err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			return nil, errors.Join(err, f.Close())
		}
		select {
		case <-ctx.Done():
			return nil, errors.Join(fmt.Errorf("waiting for the shelf's writer lock %s: %w", path, ctx.Err()), f.Close())
		case <-time.After(lockRetry):
		}
	}
	return func() error {
		return errors.Join(syscall.Flock(int(f.Fd()), syscall.LOCK_UN), f.Close())
	}, nil
}
