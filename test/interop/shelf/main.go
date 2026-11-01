// SPDX-License-Identifier: Apache-2.0

// Command shelf is Go's side of the shelf storage interlock
// (evidencebook#254 §5 item 2): CLL's storage-interlock one layer up. Each
// language runs the scripted shelf (internal/shelfscript) with the same host
// parameters; one writes it, another continues it, and the dumps of every
// pair must equal the Go run's, byte for byte (testdata/shelf/after-write.json
// and after-continue.json).
//
//	shelf write DIR       the writer phase, on a new DIR/shelf.db
//	shelf continue DIR    the continuer phase, on DIR/shelf.db
//	shelf dump DIR        the file's dump on stdout (it reads a copy)
//	shelf hold DIR SECS   open the shelf on DIR/shelf.db, print "held", keep
//	                      it open SECS seconds
//	shelf try DIR MS      open the shelf, waiting at most MS milliseconds for
//	                      its lock: exit 0 if it opened, 3 if another writer
//	                      holds the lock
//
// hold and try are the lock exchange: a Go and a Rust writer of one file
// exclude each other because both take flock(LOCK_EX) on DIR/shelf.db.lock.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/action-state-group/evidencebook/internal/shelfscript"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "shelf:", err)
		var held lockHeld
		if errors.As(err, &held) {
			os.Exit(3)
		}
		os.Exit(1)
	}
}

type lockHeld struct{ error }

func run(args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: shelf write|continue|dump|hold|try DIR [ARG]")
	}
	ctx := context.Background()
	dir := args[1]
	switch args[0] {
	case "write":
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
		return shelfscript.Write(ctx, dir)
	case "continue":
		return shelfscript.Continue(ctx, dir)
	case "dump":
		out, err := shelfscript.Dump(ctx, dir)
		if err != nil {
			return err
		}
		_, err = os.Stdout.Write(out)
		return err
	case "hold", "try":
		if len(args) != 3 {
			return fmt.Errorf("usage: shelf %s DIR N", args[0])
		}
		n, err := strconv.Atoi(args[2])
		if err != nil {
			return err
		}
		if args[0] == "hold" {
			release, err := shelfscript.Hold(ctx, dir)
			if err != nil {
				return err
			}
			fmt.Println("held")
			time.Sleep(time.Duration(n) * time.Second)
			return release()
		}
		wait, cancel := context.WithTimeout(ctx, time.Duration(n)*time.Millisecond)
		defer cancel()
		release, err := shelfscript.Hold(wait, dir)
		if errors.Is(err, context.DeadlineExceeded) {
			return lockHeld{err}
		}
		if err != nil {
			return err
		}
		return release()
	}
	return fmt.Errorf("unknown command %q", args[0])
}
