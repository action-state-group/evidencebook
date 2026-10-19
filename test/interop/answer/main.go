// SPDX-License-Identifier: Apache-2.0

// Command answer is a test responder for anyone checking evidencebook with
// their own verifier (docs/THIRD-PARTY-VERIFIER.md). It answers one
// evidence request, given as the request octets exactly as the requester
// encoded them, with the signed reply octets that evidencebook's Respond
// returns.
//
//	go run ./test/interop/answer -info
//	go run ./test/interop/answer -media-type application/json -requester alice -in req.json -out reply.json
//
// Every run opens a new book in a temporary directory and seeds it the same
// way: seven observation records, a checkpoint after the third and another
// after the seventh. The keys are Ed25519 test keys whose seeds are the
// SHA-256 of public labels (the profile's conformance-vector responder and
// log keys), so the records, the checkpoints and the keys are the same on
// every run and every machine, and -info prints them for the caller to pin.
// Only the reply's issued_at (the time of the run) and request_digest (the
// caller's own octets) change from run to run; the canonical Artifact never
// depends on the requester, the nonce, the encoding or the run.
//
// -requester stands in for a transport's authentication: it is the identity
// a transport would have authenticated. Without it only the checkpoints
// subject and the history_card/1 derivation are answered, and every other
// subject is refused not_authorized. -decline answers every request with
// the responder's cost-policy refusal, policy_declined.
//
// The book's keys are test keys. Never use them for anything else.
package main

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/action-state-group/evidencebook"
)

const (
	responderLabel = "draft-mih-agent-evidence-request-00 conformance vectors: primary responder key"
	logLabel       = "evidence-interop/0 worked example: CLL log key"
	logID          = "evidence-stream-1"
)

// seedStart is the time of the first seeded record. Seeding advances one
// second per clock reading, so the book is the same on every run.
var seedStart = time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)

// bindings are the bindings the manifest names: those evidencebook answers
// over. This command itself takes the octets from a file or stdin.
var bindings = []string{evidencebook.BindingStream, evidencebook.BindingHTTP, evidencebook.BindingA2A, evidencebook.BindingMCP}

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "answer:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdin io.Reader, stdout io.Writer) error {
	flags := flag.NewFlagSet("answer", flag.ContinueOnError)
	showInfo := flags.Bool("info", false, "print the book's keys, records, checkpoints and manifest as JSON, and exit")
	mediaType := flags.String("media-type", "", "the request's media type: application/json or application/cbor")
	requester := flags.String("requester", "", "the identity a transport would have authenticated; empty for an unauthenticated caller")
	decline := flags.Bool("decline", false, "answer from a responder whose cost policy declines every request")
	in := flags.String("in", "-", "file holding the request octets; - for stdin")
	out := flags.String("out", "-", "file to write the reply octets to; - for stdout")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments %q", flags.Args())
	}
	if *showInfo {
		data, err := info(ctx)
		if err != nil {
			return err
		}
		return write(*out, stdout, append(data, '\n'))
	}
	if *mediaType != evidencebook.MediaTypeJSON && *mediaType != evidencebook.MediaTypeCBOR {
		return fmt.Errorf("-media-type must be %s or %s", evidencebook.MediaTypeJSON, evidencebook.MediaTypeCBOR)
	}
	var octets []byte
	var err error
	if *in == "-" {
		octets, err = io.ReadAll(stdin)
	} else {
		octets, err = os.ReadFile(*in)
	}
	if err != nil {
		return err
	}
	reply, err := answer(ctx, evidencebook.Message{Octets: octets, MediaType: *mediaType}, *requester, *decline, liveClock)
	if err != nil {
		return err
	}
	return write(*out, stdout, reply)
}

func write(path string, stdout io.Writer, data []byte) error {
	if path == "-" {
		_, err := stdout.Write(data)
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// liveClock is the time a reply is issued at.
func liveClock() time.Time { return time.Now().UTC() }

func seeded(label string) ed25519.PrivateKey {
	seed := sha256.Sum256([]byte(label))
	return ed25519.NewKeyFromSeed(seed[:])
}

type declineAll struct{}

func (declineAll) Allow(string, time.Time) bool { return false }

// seededBook is a book opened in its own temporary directory and seeded.
type seededBook struct {
	book    *evidencebook.Book
	dir     string
	records []evidencebook.Record
	// checkpoints are the one after the third record and the one after the
	// seventh, in that order.
	checkpoints []evidencebook.Checkpoint
}

func (s *seededBook) close() {
	if s.book != nil {
		_ = s.book.Release()
	}
	_ = os.RemoveAll(s.dir)
}

// seed opens a new book and seeds it. Every clock reading while seeding is
// one second after the last, from seedStart; once seeding is done the book
// reads issue, the clock replies are issued by.
func seed(ctx context.Context, policy evidencebook.ResponderPolicy, issue func() time.Time) (*seededBook, error) {
	dir, err := os.MkdirTemp("", "evidencebook-answer-")
	if err != nil {
		return nil, err
	}
	s := &seededBook{dir: dir}
	ok := false
	defer func() {
		if !ok {
			s.close()
		}
	}()
	store, err := evidencebook.OpenFileStore(filepath.Join(dir, "records"))
	if err != nil {
		return nil, err
	}
	substrate, err := evidencebook.OpenCLL(filepath.Join(dir, "cll.jsonl"), logID, seeded(logLabel))
	if err != nil {
		return nil, err
	}
	signer, err := evidencebook.NewEd25519Signer(seeded(responderLabel))
	if err != nil {
		return nil, err
	}
	originals, err := evidencebook.OpenOriginalsFile(ctx, filepath.Join(dir, "originals.jsonl"), "interop", signer.PublicKey())
	if err != nil {
		return nil, err
	}
	clock, seeding := seedStart, true
	now := func() time.Time {
		if !seeding {
			return issue()
		}
		clock = clock.Add(time.Second)
		return clock
	}
	if s.book, err = evidencebook.Open(ctx, evidencebook.Config{
		BookID: "answer", Operator: "answer-operator", Store: store, Substrate: substrate, Originals: originals, Signer: signer,
		Now: now, Responder: policy,
	}); err != nil {
		return nil, err
	}
	for i := range 7 {
		r, err := s.book.Append(ctx, evidencebook.Entry{RecordType: "observation", EpistemicType: evidencebook.ObservedEvent, SubjectRef: fmt.Sprintf("order-%d", i)})
		if err != nil {
			return nil, err
		}
		s.records = append(s.records, r)
		if i == 2 || i == 6 {
			cp, err := s.book.Checkpoint(ctx)
			if err != nil {
				return nil, err
			}
			s.checkpoints = append(s.checkpoints, cp)
		}
	}
	seeding, ok = false, true
	return s, nil
}

// answer seeds a book, answers request as the authenticated requester (""
// for none), and returns the reply octets.
func answer(ctx context.Context, request evidencebook.Message, requester string, decline bool, issue func() time.Time) ([]byte, error) {
	policy := evidencebook.ResponderPolicy{}
	if decline {
		policy.Limiter = declineAll{}
	}
	s, err := seed(ctx, policy, issue)
	if err != nil {
		return nil, err
	}
	defer s.close()
	reply, _, err := s.book.Respond(ctx, request, evidencebook.RespondOptions{AuthenticatedRequester: requester, Policy: evidencebook.DefaultSharePolicy()})
	if err != nil {
		return nil, err
	}
	return reply.Octets, nil
}

// logKey is a log key pin in the form the profile's trust policy names one:
// the checkpoint's issuer, subject and key identifier.
type logKey struct {
	Iss string `json:"iss"`
	Sub string `json:"sub"`
	Kid string `json:"kid"`
}

type checkpointInfo struct {
	// Pin is the digest a request names as coverage.expected_pin: SHA-256
	// over the checkpoint's signed statement octets.
	Pin      string `json:"expected_pin"`
	TreeSize uint64 `json:"tree_size"`
	Seeded   int    `json:"seeded_records_covered"`
	LogKey   logKey `json:"log_key"`
}

type bookInfo struct {
	Note         string           `json:"note"`
	ResponderKey string           `json:"responder_key"`
	Checkpoints  []checkpointInfo `json:"checkpoints"`
	Records      []string         `json:"records"`
	Manifest     json.RawMessage  `json:"manifest"`
}

// info describes the seeded book: what a caller pins and what it can ask.
func info(ctx context.Context) ([]byte, error) {
	s, err := seed(ctx, evidencebook.ResponderPolicy{}, func() time.Time { return seedStart })
	if err != nil {
		return nil, err
	}
	defer s.close()
	manifest, err := s.book.Manifest(evidencebook.ManifestOptions{Bindings: bindings})
	if err != nil {
		return nil, err
	}
	signer, ok := seeded(responderLabel).Public().(ed25519.PublicKey)
	if !ok {
		return nil, errors.New("responder key is not Ed25519")
	}
	out := bookInfo{
		Note:         "Test keys from public labels; never use them for anything else. Pin them only as your own trust choice for this test.",
		ResponderKey: hex.EncodeToString(signer),
		Manifest:     manifest,
	}
	for i, cp := range s.checkpoints {
		sum := sha256.Sum256(cp.Statement)
		out.Checkpoints = append(out.Checkpoints, checkpointInfo{
			Pin: hex.EncodeToString(sum[:]), TreeSize: cp.TreeSize, Seeded: []int{3, 7}[i],
			LogKey: logKey{Iss: cp.LogID, Sub: fmt.Sprintf("%s#%d", cp.LogID, cp.TreeSize), Kid: cp.KeyID},
		})
	}
	for _, r := range s.records {
		out.Records = append(out.Records, r.RecordID)
	}
	return json.MarshalIndent(out, "", "  ")
}
