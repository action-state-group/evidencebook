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
//	shelf subject DIR     the subject phase on DIR/shelf.db, after continue; its
//	                      outcomes must be testdata/shelf/subject.json's
//	shelf handover DIR    the handover of the shelf at its newest stamp on stdout
//	                      (evidencebook#461 §2.7; it reads a copy)
//	shelf verify-handover FILE
//	                      replay the handover in FILE; its result on stdout
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
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
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
		return fmt.Errorf("usage: shelf write|continue|migrate|recover|plant-tick|plant-migration|plant-migration-checkpointed|plant-fields|dump|handover|record-time|subject|hold|try DIR [ARG], or shelf verify-handover FILE")
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
	case "migrate":
		return shelfscript.Migrate(ctx, dir)
	case "recover":
		return shelfscript.Recover(ctx, dir)
	case "plant-tick":
		return shelfscript.PlantTick(ctx, dir)
	case "plant-migration":
		return shelfscript.PlantMigration(ctx, dir, 2)
	case "plant-migration-checkpointed":
		return shelfscript.PlantMigrationCheckpointed(ctx, dir)
	case "plant-fields":
		return shelfscript.PlantFields(ctx, dir)
	case "handover":
		out, err := shelfscript.HandoverJSON(ctx, dir)
		if err != nil {
			return err
		}
		_, err = os.Stdout.Write(out)
		return err
	case "record-time":
		// The record time phase on a shelf after the migrate phase: its
		// receipts and outcomes must be testdata/shelf/record-time.json's
		// (run from the repository root), byte for byte.
		got, err := shelfscript.RecordTimeJSON(ctx, dir)
		if err != nil {
			return err
		}
		want, err := os.ReadFile(filepath.Join("testdata", "shelf", "record-time.json"))
		if err != nil {
			return err
		}
		if !bytes.Equal(got, want) {
			return fmt.Errorf("the record time phase differs from testdata/shelf/record-time.json")
		}
		return nil
	case "subject":
		// The subject phase on a shelf after the continue phase: its
		// outcomes must be testdata/shelf/subject.json's (run from the
		// repository root), byte for byte; the dump afterwards is
		// after-subject.json.
		got, err := shelfscript.SubjectJSON(ctx, dir)
		if err != nil {
			return err
		}
		want, err := os.ReadFile(filepath.Join("testdata", "shelf", "subject.json"))
		if err != nil {
			return err
		}
		if !bytes.Equal(got, want) {
			return fmt.Errorf("the subject phase differs from testdata/shelf/subject.json")
		}
		return nil
	case "verify-handover":
		raw, err := os.ReadFile(dir)
		if err != nil {
			return err
		}
		out, err := shelfscript.VerifyHandoverJSON(raw)
		if err != nil {
			return err
		}
		_, err = os.Stdout.Write(out)
		return err
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
