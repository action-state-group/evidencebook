// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
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
			<-store.entered
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
	<-store.entered
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
				statement, err := verifyReply(sent, reply, keys)
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

// slowStore makes every answer cost answerCost: each one reads the
// checkpoint history once to build its view, as an answer over a large book
// spends its time reading the store and loading the log. Sealing never
// reads it.
type slowStore struct {
	Store
	answerCost time.Duration
}

func (s *slowStore) Checkpoints(ctx context.Context) ([]Checkpoint, error) {
	time.Sleep(s.answerCost)
	return s.Store.Checkpoints(ctx)
}

// answerCost is what every answer costs under the flood, and sealingBound
// the stated bound on one seal's latency. A seal waits at most for the seals
// queued ahead of it, one per answer in flight, each a few fsynced journal
// writes; the bound leaves room for those on a loaded runner under the race
// detector. Holding the book's lock for each answer, as Respond did before
// #208, made every seal wait out at least the answer in progress, and in
// practice the answers queued ahead of it: seconds.
const (
	answerCost   = time.Second
	sealingBound = answerCost / 2
)

// TestRespondFloodDoesNotStallSealing is #208's load test: flooders keep
// requests in flight continuously while the book seals at a fixed rate for
// two answer costs, so every flooder completes an answer (and, when
// authenticated, seals its record) while it is measured. Every seal must
// land, in order, and none may take longer than sealingBound. It runs once with strangers (checkpoints, history_card/1 and
// a refused full_history) and once with an authenticated requester, whose
// answers each also seal a record under the lock.
func TestRespondFloodDoesNotStallSealing(t *testing.T) {
	const (
		history  = 40
		flooders = 8
		window   = 2 * answerCost
		interval = 5 * time.Millisecond
	)
	for _, c := range []struct {
		name      string
		requester string
		requests  func(*testing.T, *Book) []Message
	}{
		{"strangers", "", func(t *testing.T, _ *Book) []Message { return strangerRequests(t) }},
		{"authenticated", "peer-1", func(t *testing.T, book *Book) []Message {
			return []Message{
				jsonRequest(t, EvidenceRequest{Subject: Subject{Kind: SubjectRecord, Digest: book.log[0].RecordID}, Coverage: fresh()}),
				jsonRequest(t, EvidenceRequest{Subject: Subject{Kind: SubjectFullHistory}, Derivation: DerivationHistoryCard, Coverage: fresh()}),
				jsonRequest(t, EvidenceRequest{Subject: Subject{Kind: SubjectCorrelation, Correlation: "order-0"}, Coverage: fresh()}),
			}
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			ctx := context.Background()
			f := newFixture(t, "book-a", 1)
			cfg := f.config(t)
			store := &slowStore{Store: cfg.Store}
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
			store.answerCost = answerCost
			requests := c.requests(t, book)

			stop := make(chan struct{})
			var answered atomic.Int64
			var flood sync.WaitGroup
			for w := range flooders {
				flood.Go(func() {
					for i := w; ; i++ {
						select {
						case <-stop:
							return
						default:
						}
						if _, _, err := book.Respond(ctx, requests[i%len(requests)], RespondOptions{AuthenticatedRequester: c.requester, Policy: policyWith("peers")}); err != nil {
							t.Error(err)
							return
						}
						answered.Add(1)
					}
				})
			}

			var latencies []time.Duration
			var ids []string
			tick := time.NewTicker(interval)
			end := time.Now().Add(window)
			for i := 0; time.Now().Before(end); i++ {
				<-tick.C
				start := time.Now()
				record, err := book.Append(ctx, observation(fmt.Sprintf("sealed-%d", i), `{"status":"approved"}`))
				latencies = append(latencies, time.Since(start))
				if err != nil {
					t.Fatal(err)
				}
				ids = append(ids, record.RecordID)
				if i%10 == 9 {
					if _, err := book.Checkpoint(ctx); err != nil {
						t.Fatal(err)
					}
				}
			}
			tick.Stop()
			during := answered.Load()
			close(stop)
			flood.Wait()

			slices.Sort(latencies)
			worst := latencies[len(latencies)-1]
			t.Logf("%d answers during %d seals; seal latency p50 %s, max %s (bound %s)", during, len(latencies), latencies[len(latencies)/2], worst, sealingBound)
			if during < flooders {
				t.Fatalf("only %d answers finished while sealing was measured; the flood did not run through it", during)
			}
			if worst > sealingBound {
				t.Fatalf("a seal took %s under the flood, over the %s bound", worst, sealingBound)
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
	<-store.entered
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
