// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Respond answers from a snapshot of the log and holds the book's lock only
// to take it, to issue an anchor and to seal its own record, so a flood of
// requests never stalls the book's own sealing (evidencebook#208).

// gatedStore blocks Checkpoints, which Respond reads to build its view, while
// armed, until the test releases it.
type gatedStore struct {
	Store
	armed   atomic.Bool
	entered chan struct{}
	release chan struct{}
}

func (s *gatedStore) Checkpoints(ctx context.Context) ([]Checkpoint, error) {
	if s.armed.CompareAndSwap(true, false) {
		close(s.entered)
		<-s.release
	}
	return s.Store.Checkpoints(ctx)
}

// waitEntered waits for the gated answer to reach the store, failing rather
// than hanging when it never does.
func (s *gatedStore) waitEntered(t *testing.T) {
	t.Helper()
	select {
	case <-s.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the answer never reached the store")
	}
}

// strangerRequests are the requests anyone may send: the checkpoints
// subject, the history_card/1 derivation, and one form refused to every
// stranger.
func strangerRequests(t *testing.T) []Message {
	t.Helper()
	return []Message{
		jsonRequest(t, EvidenceRequest{Subject: Subject{Kind: SubjectCheckpoints}, Coverage: fresh()}),
		jsonRequest(t, EvidenceRequest{Subject: Subject{Kind: SubjectFullHistory}, Derivation: DerivationHistoryCard, Coverage: fresh()}),
		jsonRequest(t, EvidenceRequest{Subject: Subject{Kind: SubjectFullHistory}, Coverage: fresh()}),
	}
}

// within fails the test unless f returns within d.
func within(t *testing.T, d time.Duration, what string, f func() error) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- f() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("%s: %v", what, err)
		}
	case <-time.After(d):
		t.Fatalf("%s did not finish within %s while an answer was in progress", what, d)
	}
}

// TestRespondDoesNotHoldTheLockWhileAnswering stops an answer partway
// through and seals, checkpoints and records a received half meanwhile.
// Under the book lock for the whole answer, each of them would wait for it.
func TestRespondDoesNotHoldTheLockWhileAnswering(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, "book-a", 1)
	cfg := f.config(t)
	store := &gatedStore{Store: cfg.Store, entered: make(chan struct{}), release: make(chan struct{})}
	cfg.Store = store
	book, err := Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = book.Release() })
	mustAppend(t, book, observation("order-17", `{"status":"approved"}`))
	anchorPin(t, book)

	for _, c := range []struct {
		name      string
		requester string
		request   Message
	}{
		{"stranger checkpoints", "", strangerRequests(t)[0]},
		{"stranger history card", "", strangerRequests(t)[1]},
		{"authenticated record", "peer-1", jsonRequest(t, EvidenceRequest{Subject: Subject{Kind: SubjectRecord, Digest: book.log[0].RecordID}, Coverage: fresh()})},
	} {
		t.Run(c.name, func(t *testing.T) {
			store.entered, store.release = make(chan struct{}), make(chan struct{})
			store.armed.Store(true)
			// Let the answer finish whether or not the test fails, so a
			// failure reports instead of hanging on the book's lock.
			release := sync.OnceFunc(func() { close(store.release) })
			defer release()
			replied := make(chan Reply, 1)
			go func() {
				reply, _, err := book.Respond(ctx, c.request, RespondOptions{AuthenticatedRequester: c.requester, Policy: policyWith("peers")})
				if err != nil {
					t.Error(err)
				}
				replied <- reply
			}()
			store.waitEntered(t)
			within(t, 10*time.Second, "Append", func() error {
				_, err := book.Append(ctx, observation("order-18", `{"status":"held"}`))
				return err
			})
			within(t, 10*time.Second, "Checkpoint", func() error {
				_, err := book.Checkpoint(ctx)
				return err
			})
			// A pushed half is received as an Append of the counterparty's
			// record citing it.
			within(t, 10*time.Second, "receiving a pushed half", func() error {
				_, err := book.Append(ctx, Entry{RecordType: "exchange", EpistemicType: ObservedEvent, CounterpartyRef: "peer-2", Links: []Link{{Type: Cites, Target: digestBytes([]byte("pushed half"))}}})
				return err
			})
			release()
			if reply := <-replied; reply.Envelope == nil {
				t.Fatalf("the answer was refused: %+v", reply.Refusal)
			}
		})
	}
}

// TestReleaseWaitsForAnswersInProgress stops an answer partway and releases
// the book: Release must wait for the answer, which completes, rather than
// close the store and substrate under it.
func TestReleaseWaitsForAnswersInProgress(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, "book-a", 1)
	cfg := f.config(t)
	store := &gatedStore{Store: cfg.Store, entered: make(chan struct{}), release: make(chan struct{})}
	cfg.Store = store
	book, err := Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	record := mustAppend(t, book, observation("order-17", `{"status":"approved"}`))
	anchorPin(t, book)
	store.armed.Store(true)
	release := sync.OnceFunc(func() { close(store.release) })
	defer release()
	replied := make(chan error, 1)
	go func() {
		reply, sealed, err := book.Respond(ctx, jsonRequest(t, EvidenceRequest{Subject: Subject{Kind: SubjectRecord, Digest: record.RecordID}, Coverage: fresh()}), RespondOptions{AuthenticatedRequester: "peer-1", Policy: policyWith("peers")})
		if err == nil && (reply.Envelope == nil || sealed.RecordID == "") {
			err = fmt.Errorf("want an answer and its sealed record, got refusal %+v", reply.Refusal)
		}
		replied <- err
	}()
	store.waitEntered(t)
	released := make(chan error, 1)
	go func() { released <- book.Release() }()
	select {
	case err := <-released:
		t.Fatalf("Release returned (%v) while an answer was in progress", err)
	case <-time.After(100 * time.Millisecond):
	}
	release()
	if err := <-replied; err != nil {
		t.Fatalf("the answer in progress failed: %v", err)
	}
	if err := <-released; err != nil {
		t.Fatal(err)
	}
}

// TestRespondAnswersStayConsistentWhileTheLogGrows answers while another
// goroutine seals and checkpoints. Every answer must verify against the
// checkpoint it cites: its records, proofs and anchor all come from one
// snapshot, never from a log that grew under it.
func TestRespondAnswersStayConsistentWhileTheLogGrows(t *testing.T) {
	ctx := context.Background()
	book, records := linkedBook(t)
	anchorPin(t, book)
	keys := responderKeys(book, 1)

	stop := make(chan struct{})
	sealed := make(chan error, 1)
	go func() {
		defer close(sealed)
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			if _, err := book.Append(ctx, observation(fmt.Sprintf("order-%d", i), `{"status":"approved"}`)); err != nil {
				sealed <- err
				return
			}
			if i%3 == 0 {
				if _, err := book.Checkpoint(ctx); err != nil {
					sealed <- err
					return
				}
			}
		}
	}()

	requests := []struct {
		requester string
		request   EvidenceRequest
	}{
		{"", EvidenceRequest{Subject: Subject{Kind: SubjectCheckpoints}, Coverage: fresh()}},
		{"", EvidenceRequest{Subject: Subject{Kind: SubjectFullHistory}, Derivation: DerivationHistoryCard, Coverage: fresh()}},
		{"peer-1", EvidenceRequest{Subject: Subject{Kind: SubjectRecord, Digest: records[1].RecordID}, Coverage: fresh()}},
		{"peer-1", EvidenceRequest{Subject: Subject{Kind: SubjectExchange, Digest: records[0].RecordID}, Coverage: fresh()}},
	}
	var wg sync.WaitGroup
	for w := range 4 {
		wg.Go(func() {
			for i := range 15 {
				r := requests[(w+i)%len(requests)]
				msg := jsonRequest(t, r.request)
				reply, _, err := book.Respond(ctx, msg, RespondOptions{AuthenticatedRequester: r.requester, Policy: policyWith("peers")})
				if err != nil {
					t.Error(err)
					return
				}
				if reply.Envelope == nil {
					t.Errorf("%s was refused: %+v", r.request.Subject.Kind, reply.Refusal)
					return
				}
				request := r.request
				request.Profile = ProfileID
				sent := sentRequest{digest: digestBytes(msg.Octets), request: request}
				statement, err := verifyReply(sent, reply, keys, nil)
				if err != nil || statement.Outcome != OutcomeArtifact {
					t.Errorf("%s: the answer does not verify against its anchor: %v %s", r.request.Subject.Kind, err, statement.Failure)
					return
				}
			}
		})
	}
	wg.Wait()
	close(stop)
	if err := <-sealed; err != nil {
		t.Fatal(err)
	}
}

// turnstileStore parks every answer in its view read (Checkpoints, which
// Respond reads outside the book's lock) until the test opens the turnstile,
// so the test, not the host's speed, decides when answers are in flight.
// Sealing never reads Checkpoints, so a seal never parks.
type turnstileStore struct {
	Store
	stop chan struct{} // closed: nothing parks any more
	mu   sync.Mutex
	turn chan struct{} // nil until armed; closed to let the parked answers through
	// parked counts the answers waiting at turn.
	parked int
}

func (s *turnstileStore) Checkpoints(ctx context.Context) ([]Checkpoint, error) {
	s.mu.Lock()
	turn := s.turn
	if turn != nil {
		s.parked++
	}
	s.mu.Unlock()
	if turn != nil {
		select {
		case <-turn:
		case <-s.stop:
		}
	}
	return s.Store.Checkpoints(ctx)
}

// arm makes every later answer park.
func (s *turnstileStore) arm() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.turn = make(chan struct{})
}

// open lets the parked answers through; the next answers park again.
func (s *turnstileStore) open() {
	s.mu.Lock()
	defer s.mu.Unlock()
	close(s.turn)
	s.turn, s.parked = make(chan struct{}), 0
}

// waitParked waits until n answers are parked, failing rather than hanging
// when they never are.
func (s *turnstileStore) waitParked(t *testing.T, n int, bound time.Duration) {
	t.Helper()
	for deadline := time.Now().Add(bound); ; time.Sleep(time.Millisecond) {
		s.mu.Lock()
		parked := s.parked
		s.mu.Unlock()
		if parked >= n {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("only %d of %d answers reached the store within %s: an answer in progress holds the others back, as one holding the book's lock would", parked, n, bound)
		}
	}
}

// TestRespondFloodDoesNotStallSealing is #208's load test, asserted as
// progress rather than time: flooders keep a request each in flight, and the
// book seals while every one of them is in the middle of its answer. A
// turnstile store parks each answer in its view read until the test lets it
// through after a seal. Each round waits until all the flooders are parked,
// seals while every answer is in flight, opens the turnstile, and seals
// again at once, racing the answers released, which, when authenticated,
// seal their own records under the lock. Had Respond held the book's lock
// for its answer, as before #208, the first parked answer would hold it and
// the others could never reach the store, so the round never starts. Every
// answer completes only after a seal landed while it was in flight, exactly
// flooders answers complete per round, and every seal lands, in order.
//
// The one timer, hang, catches a deadlock, and load does not reach it. On a
// loaded host under the race detector a seal takes up to about two and a
// half seconds in its own durable writes, and it waits only on other seals,
// never on an answer in progress (evidencebook#938). The longest wait is
// for the racing seal and the records of the flooders' released answers,
// one seal each: about 22 seconds there. The test asserts no latency, which
// measures the host.
// Like TestRespondDoesNotHoldTheLockWhileAnswering, the single-answer check,
// it holds answers in their view read, so it shows the lock is not held
// there. It runs once with strangers (checkpoints, history_card/1 and a
// refused full_history), whose answers never seal, and once with an
// authenticated requester, whose answers each seal a record.
func TestRespondFloodDoesNotStallSealing(t *testing.T) {
	const (
		history  = 40
		flooders = 8
		rounds   = 10
		hang     = 2 * time.Minute
	)
	for _, c := range []struct {
		name      string
		requester string
		requests  func(*testing.T, *Book) []Message
		// refused is the index of the one request refused, -1 for none.
		refused int
	}{
		{"strangers", "", func(t *testing.T, _ *Book) []Message { return strangerRequests(t) }, 2},
		{"authenticated", "peer-1", func(t *testing.T, book *Book) []Message {
			return []Message{
				jsonRequest(t, EvidenceRequest{Subject: Subject{Kind: SubjectRecord, Digest: book.log[0].RecordID}, Coverage: fresh()}),
				jsonRequest(t, EvidenceRequest{Subject: Subject{Kind: SubjectFullHistory}, Derivation: DerivationHistoryCard, Coverage: fresh()}),
				jsonRequest(t, EvidenceRequest{Subject: Subject{Kind: SubjectCorrelation, Correlation: "order-0"}, Coverage: fresh()}),
			}
		}, -1},
	} {
		t.Run(c.name, func(t *testing.T) {
			ctx := context.Background()
			f := newFixture(t, "book-a", 1)
			cfg := f.config(t)
			store := &turnstileStore{Store: cfg.Store, stop: make(chan struct{})}
			cfg.Store = store
			book, err := Open(ctx, cfg)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = book.Release() })
			for i := range history {
				mustAppend(t, book, observation(fmt.Sprintf("order-%d", i%10), `{"status":"approved"}`))
				if i%4 == 3 {
					anchorPin(t, book)
				}
			}
			requests := c.requests(t, book)
			size := book.Size()

			store.arm()
			var answered atomic.Int64
			var flood sync.WaitGroup
			// However the test ends, the parked answers go through and the
			// flooders stop before the book is released.
			stop := sync.OnceFunc(func() { close(store.stop) })
			t.Cleanup(func() { stop(); flood.Wait() })
			for w := range flooders {
				flood.Go(func() {
					for i := w; ; i++ {
						select {
						case <-store.stop:
							return
						default:
						}
						k := i % len(requests)
						reply, sealed, err := book.Respond(ctx, requests[k], RespondOptions{AuthenticatedRequester: c.requester, Policy: policyWith("peers")})
						switch {
						case err != nil:
							t.Error(err)
							return
						case (k == c.refused) != (reply.Refusal != nil) || (k == c.refused) == (reply.Envelope != nil):
							t.Errorf("request %d: want refused %t, got envelope %t, refusal %+v", k, k == c.refused, reply.Envelope != nil, reply.Refusal)
							return
						case (c.requester == "") != (sealed.RecordID == ""):
							t.Errorf("request %d by %q: sealed record %q; only an authenticated requester's answer seals one", k, c.requester, sealed.RecordID)
							return
						}
						answered.Add(1)
					}
				})
			}

			var ids []string
			var slowest time.Duration
			var roots uint64
			seal := func(name string) {
				t.Helper()
				within(t, hang, "sealing "+name, func() error {
					start := time.Now()
					record, err := book.Append(ctx, observation(name, `{"status":"approved"}`))
					slowest = max(slowest, time.Since(start))
					ids = append(ids, record.RecordID)
					return err
				})
			}
			for r := range rounds {
				store.waitParked(t, flooders, hang)
				// Every flooder's earlier answer completed before it parked
				// again, and none completes while parked.
				if got, want := answered.Load(), int64(r*flooders); got != want {
					t.Fatalf("round %d: %d answers completed, want %d", r, got, want)
				}
				seal(fmt.Sprintf("held-%d", r))
				if r%3 == 2 {
					// The answers are parked, so the checkpoint is the
					// only writer: what it adds is its index roots.
					before := book.Size()
					within(t, hang, "Checkpoint", func() error {
						_, err := book.Checkpoint(ctx)
						return err
					})
					roots += book.Size() - before
				}
				store.open()
				seal(fmt.Sprintf("racing-%d", r))
			}
			store.waitParked(t, flooders, hang)
			during := answered.Load()
			stop()
			flood.Wait()
			if want := int64(rounds * flooders); during != want {
				t.Fatalf("%d answers completed in %d rounds, want %d", during, rounds, want)
			}
			t.Logf("%d answers and %d seals; slowest seal %s", during, len(ids), slowest)

			// Strangers never grow the log; each authenticated answer seals
			// one record. Every answer has completed: the flooders stopped.
			sealedByAnswers := uint64(0)
			if c.requester != "" {
				sealedByAnswers = uint64(answered.Load())
			}
			if got, want := book.Size(), size+uint64(len(ids))+roots+sealedByAnswers; got != want {
				t.Fatalf("the book holds %d records, want %d: %d before, %d seals, %d index roots, %d answers recorded", got, want, size, len(ids), roots, sealedByAnswers)
			}
			// No seal is lost: each is in the reopened book, in the order
			// it was sealed.
			if err := book.Release(); err != nil {
				t.Fatal(err)
			}
			reopened := f.open(t)
			var last uint64
			for _, id := range ids {
				record, err := reopened.Get(ctx, id)
				if err != nil {
					t.Fatalf("seal %s was lost: %v", id, err)
				}
				if record.Seq <= last {
					t.Fatalf("seal %s is at %d, not after %d", id, record.Seq, last)
				}
				last = record.Seq
			}
		})
	}
}

// TestRespondRestoresTheLogOncePerAnswer: each restore reads the whole log
// under the log store's lock, which appends also take, so a history_card/1
// answer over many checkpoints restores it once, not once per proof.
func TestRespondRestoresTheLogOncePerAnswer(t *testing.T) {
	book := newFixture(t, "book-a", 1).open(t)
	for i := range 6 {
		mustAppend(t, book, observation(fmt.Sprintf("order-%d", i), `{"status":"approved"}`))
		anchorPin(t, book)
	}
	substrate := book.commitments.(*CLLSubstrate)
	substrate.mu.Lock()
	before := substrate.treeLoads
	substrate.mu.Unlock()
	mustEnvelope(t, respondAs(t, book, strangerRequests(t)[1], ""))
	substrate.mu.Lock()
	defer substrate.mu.Unlock()
	if loads := substrate.treeLoads - before; loads != 1 {
		t.Fatalf("one history card over 6 checkpoints restored the log %d times", loads)
	}
}

// TestRespondRelationshipIsFromTheSnapshotItAnswersFrom: when an answer
// issues an anchor it takes a second snapshot, and the requester's
// relationship is read from that one, the snapshot the answer and its
// anchor come from. Here a record naming the requester as its counterparty
// is sealed between the two snapshots.
func TestRespondRelationshipIsFromTheSnapshotItAnswersFrom(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, "book-a", 1)
	cfg := f.config(t)
	store := &gatedStore{Store: cfg.Store, entered: make(chan struct{}), release: make(chan struct{})}
	cfg.Store = store
	book, err := Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = book.Release() })
	record := mustAppend(t, book, observation("order-17", `{"status":"approved"}`))
	anchorPin(t, book)
	mustAppend(t, book, observation("order-18", `{"status":"held"}`))
	// A floor at the log's size, past the last anchor, needs an anchor
	// issued; it is issued after the counterparty record lands.
	request := jsonRequest(t, EvidenceRequest{Subject: Subject{Kind: SubjectRecord, Digest: record.RecordID}, Coverage: Coverage{MinFreshness: sizeFloor(book.Size())}})
	store.armed.Store(true)
	release := sync.OnceFunc(func() { close(store.release) })
	defer release()
	type result struct {
		reply  Reply
		sealed Record
		err    error
	}
	done := make(chan result, 1)
	go func() {
		reply, sealed, err := book.Respond(ctx, request, RespondOptions{AuthenticatedRequester: "peer-1", Policy: policyWith("peers")})
		done <- result{reply, sealed, err}
	}()
	store.waitEntered(t)
	mustAppend(t, book, Entry{RecordType: "exchange", EpistemicType: ObservedEvent, CounterpartyRef: "peer-1"})
	release()
	r := <-done
	if r.err != nil {
		t.Fatal(r.err)
	}
	mustEnvelope(t, r.reply)
	var statement AnsweredStatement
	if err := json.Unmarshal(r.sealed.Header.Statement, &statement); err != nil {
		t.Fatal(err)
	}
	if statement.Relationship != RelationshipCounterparty {
		t.Fatalf("the answer under an anchor covering a record naming the requester calls it %s", statement.Relationship)
	}
}

// TestRespondIgnoresCheckpointsPastItsSnapshot: a checkpoint issued after an
// answer took its snapshot covers records the snapshot does not hold, so the
// answer never cites it.
func TestRespondIgnoresCheckpointsPastItsSnapshot(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, "book-a", 1)
	cfg := f.config(t)
	store := &gatedStore{Store: cfg.Store, entered: make(chan struct{}), release: make(chan struct{})}
	cfg.Store = store
	book, err := Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = book.Release() })
	mustAppend(t, book, observation("order-17", `{"status":"approved"}`))
	pin := anchorPin(t, book)
	store.armed.Store(true)
	release := sync.OnceFunc(func() { close(store.release) })
	defer release()
	request := strangerRequests(t)[0]
	type result struct {
		reply Reply
		err   error
	}
	replied := make(chan result, 1)
	go func() {
		reply, _, err := book.Respond(ctx, request, RespondOptions{Policy: policyWith("peers")})
		replied <- result{reply, err}
	}()
	store.waitEntered(t)
	mustAppend(t, book, observation("order-18", `{"status":"held"}`))
	if anchorPin(t, book) == pin {
		t.Fatal("the second checkpoint is the first")
	}
	release()
	r := <-replied
	if r.err != nil {
		t.Fatal(r.err)
	}
	if e := mustEnvelope(t, r.reply); e.Anchor.Digest != pin {
		t.Fatalf("the answer cites anchor %s, issued after its snapshot; want %s", e.Anchor.Digest, pin)
	}
}

// TestRespondIssuesOneAnchorForConcurrentFloors: answers decide from their
// own snapshots, so several can find a freshness floor unmet at once. The
// first issues the anchor and the rest answer under it.
func TestRespondIssuesOneAnchorForConcurrentFloors(t *testing.T) {
	ctx := context.Background()
	book, records := linkedBook(t)
	anchorPin(t, book)
	mustAppend(t, book, observation("order-19", `{"status":"approved"}`))
	before, err := book.Checkpoints(ctx)
	if err != nil {
		t.Fatal(err)
	}
	request := jsonRequest(t, EvidenceRequest{Subject: Subject{Kind: SubjectRecord, Digest: records[0].RecordID}, Coverage: Coverage{MinFreshness: sizeFloor(book.Size())}})
	replies := make([]Reply, 8)
	errs := make([]error, 8)
	var wg sync.WaitGroup
	for i := range replies {
		wg.Go(func() {
			replies[i], _, errs[i] = book.Respond(ctx, request, RespondOptions{AuthenticatedRequester: "peer-1", Policy: policyWith("peers")})
		})
	}
	wg.Wait()
	for i, reply := range replies {
		if errs[i] != nil {
			t.Fatal(errs[i])
		}
		mustEnvelope(t, reply)
	}
	after, err := book.Checkpoints(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if issued := len(after) - len(before); issued != 1 {
		t.Fatalf("8 concurrent answers to one floor issued %d anchors, want 1", issued)
	}
}
