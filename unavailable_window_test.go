// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/action-state-group/agent-action-capsule/go/evidencerequest"
	"github.com/action-state-group/checkpointed-local-log/go/checkpoint"
	"github.com/fxamacker/cbor/v2"
	"github.com/veraison/go-cose"
)

type windowVector struct {
	ID           string `json:"id"`
	Kind         string `json:"kind"`
	ResponderKey string `json:"responder_key"`
	Absence      struct {
		SentAt   string  `json:"sent_at"`
		ClosedAt *string `json:"closed_at"`
		Received string  `json:"received"`
	} `json:"absence"`
	Statements []struct {
		ID        string         `json:"id"`
		Statement map[string]any `json:"statement"`
		Receipts  []struct {
			Time     string `json:"time"`
			Accepted bool   `json:"accepted"`
		} `json:"receipts"`
	} `json:"statements"`
	Expect struct {
		State     string   `json:"state"`
		CoveredBy []string `json:"covered_by"`
		ShownAs   string   `json:"shown_as"`
		Labels    map[string]struct {
			ReasonClass      string    `json:"reason_class"`
			RegistrationTime string    `json:"registration_time"`
			StatedAfterwards bool      `json:"stated_afterwards"`
			StatedPeriod     [2]string `json:"stated_period"`
		} `json:"labels"`
	} `json:"expect"`
}

// TestUnavailableWindowDisplayVectors runs every display case of the
// vendored unavailable-window.json through a requester's book: the absence
// is recorded with RecordAbsence over the case's waiting window and shown
// with ShowAbsence beside the case's statements. The two cases whose request
// is not an absence (a refusal, a request still pending) have no absence
// record to show: the book records none, and ShowAbsence refuses any other
// record.
func TestUnavailableWindowDisplayVectors(t *testing.T) {
	cases := loadCases[windowVector](t, "unavailable-window.json")
	displays := 0
	for _, c := range cases {
		if c.Kind != "display" {
			continue
		}
		displays++
		t.Run(c.ID, func(t *testing.T) {
			ctx := context.Background()
			f := newFixture(t, "requester", 30)
			requester := f.open(t)
			sentAt := mustTime(t, c.Absence.SentAt)
			f.clock.t = sentAt.Add(-time.Second) // the fixture clock ticks a second per commit
			sent, err := requester.Request(ctx, EvidenceRequest{Subject: Subject{Kind: SubjectFullHistory}, Coverage: fresh()}, "book-a")
			if err != nil {
				t.Fatal(err)
			}
			if c.Absence.Received != "nothing" || c.Absence.ClosedAt == nil {
				// Not an absence: a pending request records none, and the
				// request record is not one to show.
				if c.Expect.State == "pending" {
					if _, err := requester.RecordAbsence(ctx, sent.RecordID, sentAt.Add(MinimumAbsenceWindow), ""); !errors.Is(err, ErrInvalid) {
						t.Fatalf("an absence was recorded while the request was pending: %v", err)
					}
				}
				if _, err := requester.ShowAbsence(ctx, sent.RecordID, c.ResponderKey, nil); !errors.Is(err, ErrNotFound) {
					t.Fatalf("a request record was shown as an absence: %v", err)
				}
				return
			}
			closed := mustTime(t, *c.Absence.ClosedAt)
			f.clock.t = closed.Add(time.Minute)
			absence, err := requester.RecordAbsence(ctx, sent.RecordID, closed, "")
			if err != nil {
				t.Fatal(err)
			}
			var window AbsenceRecord
			if err := json.Unmarshal(absence.Header.Statement, &window); err != nil || window.Window.Opened != c.Absence.SentAt || window.Window.Closed != *c.Absence.ClosedAt {
				t.Fatalf("the recorded window is %+v, want %s to %s (%v)", window.Window, c.Absence.SentAt, *c.Absence.ClosedAt, err)
			}
			held := len(requester.log)
			var statements []evidencerequest.WindowStatement
			for _, s := range c.Statements {
				w := evidencerequest.WindowStatement{ID: s.ID, Statement: s.Statement}
				for _, r := range s.Receipts {
					w.Receipts = append(w.Receipts, evidencerequest.RegistrationReceipt{Time: r.Time, Accepted: r.Accepted})
				}
				statements = append(statements, w)
			}
			got, err := requester.ShowAbsence(ctx, absence.RecordID, c.ResponderKey, statements)
			if err != nil {
				t.Fatal(err)
			}
			if got.State != c.Expect.State || got.ShownAs != c.Expect.ShownAs || !reflect.DeepEqual(append([]string{}, got.CoveredBy...), append([]string{}, c.Expect.CoveredBy...)) {
				t.Fatalf("shown as %q (%s) covered by %v, want %q (%s) covered by %v", got.ShownAs, got.State, got.CoveredBy, c.Expect.ShownAs, c.Expect.State, c.Expect.CoveredBy)
			}
			if len(got.Labels) != len(c.Expect.Labels) {
				t.Fatalf("labels %v, want %v", got.Labels, c.Expect.Labels)
			}
			for id, want := range c.Expect.Labels {
				label := got.Labels[id]
				if label.ReasonClass != want.ReasonClass || label.RegistrationTime != want.RegistrationTime || label.StatedAfterwards != want.StatedAfterwards || label.StatedPeriod != want.StatedPeriod {
					t.Fatalf("label %s: %+v, want %+v", id, label, want)
				}
			}
			// Showing changes nothing: the absence and the log are as they were.
			if len(requester.log) != held {
				t.Fatal("showing an absence appended a record")
			}
			again, err := requester.Get(ctx, absence.RecordID)
			if err != nil || !reflect.DeepEqual(again.Header, absence.Header) {
				t.Fatalf("the absence record changed: %v", err)
			}
		})
	}
	if displays != 16 {
		t.Fatalf("unavailable-window.json has %d display cases, want 16", displays)
	}
}

func mustTime(t *testing.T, text string) time.Time {
	t.Helper()
	at, ok := evidencerequest.ParseRequestTime(text)
	if !ok {
		t.Fatalf("time %q", text)
	}
	return at
}

func TestUnavailableWindowText(t *testing.T) {
	if UnavailableWindowText != "Not answered. The holder says these records are unavailable for this period." {
		t.Fatalf("the display wording drifted from Sherlock2's: %q", UnavailableWindowText)
	}
}

func TestSealUnavailableWindow(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, "responder", 31)
	book := f.open(t)
	start := f.clock.t.Add(-time.Hour)
	end := f.clock.t
	for _, class := range evidencerequest.UnavailableReasonClasses {
		record, err := book.SealUnavailableWindow(ctx, start, end, class)
		if err != nil {
			t.Fatalf("%s: %v", class, err)
		}
		if record.Header.RecordType != RecordTypeUnavailableWindow {
			t.Fatalf("record type %q", record.Header.RecordType)
		}
		decoded, err := evidencerequest.DecodeJSON(record.Header.Statement)
		if err != nil {
			t.Fatal(err)
		}
		// The statement is the draft's: a reader with an accepted receipt
		// recognizes it as signed by this book.
		reading := evidencerequest.RecognizeUnavailableWindow(decoded.(map[string]any), []evidencerequest.RegistrationReceipt{{Time: evidencerequest.ProfileTime(end), Accepted: true}}, KeyID(book.signer))
		if !reading.Recognized {
			t.Fatalf("%s: not recognized: %s", class, reading.Why)
		}
		// ... and not as signed by another responder.
		if other := evidencerequest.RecognizeUnavailableWindow(decoded.(map[string]any), []evidencerequest.RegistrationReceipt{{Time: evidencerequest.ProfileTime(end), Accepted: true}}, hex.EncodeToString(seededKey(99)[32:])); other.Why != evidencerequest.StatementNotSignedByResponder {
			t.Fatalf("%s: read for another responder: %+v", class, other)
		}
	}
	for name, bad := range map[string]func() (Record, error){
		"a class outside the list": func() (Record, error) { return book.SealUnavailableWindow(ctx, start, end, "ddos") },
		"an empty class":           func() (Record, error) { return book.SealUnavailableWindow(ctx, start, end, "") },
		"end before start":         func() (Record, error) { return book.SealUnavailableWindow(ctx, end, start, "flood") },
	} {
		if _, err := bad(); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%s sealed: %v", name, err)
		}
	}
	// Start equal to end is a period of one instant, which the draft allows.
	if _, err := book.SealUnavailableWindow(ctx, start, start, "outage"); err != nil {
		t.Fatalf("an instant: %v", err)
	}
}

// mintReceiptAt is mintReceipt with the witness's registration time: the iat
// of the protected CWT claims.
func mintReceiptAt(t *testing.T, statement []byte, witnessKey ed25519.PrivateKey, iat time.Time) map[string]any {
	t.Helper()
	record, err := checkpoint.ParseRecord(statement)
	if err != nil {
		t.Fatal(err)
	}
	entry, err := record.EntryHash()
	if err != nil {
		t.Fatal(err)
	}
	proof, err := cbor.Marshal([]any{int64(1), int64(0), [][]byte{}})
	if err != nil {
		t.Fatal(err)
	}
	leaf := sha256.Sum256(append([]byte{0}, entry...))
	message := cose.NewSign1Message()
	message.Headers.Protected.SetAlgorithm(cose.AlgorithmEdDSA)
	message.Headers.Protected[int64(395)] = int64(1)
	message.Headers.Protected[int64(15)] = map[any]any{int64(6): iat.Unix()}
	message.Headers.Unprotected[int64(396)] = map[any]any{int64(-1): []any{proof}}
	message.Payload = leaf[:]
	signer, err := cose.NewSigner(cose.AlgorithmEdDSA, witnessKey)
	if err != nil {
		t.Fatal(err)
	}
	if err := message.Sign(rand.Reader, nil, signer); err != nil {
		t.Fatal(err)
	}
	message.Payload = nil
	raw, err := message.MarshalCBOR()
	if err != nil {
		t.Fatal(err)
	}
	return map[string]any{
		"ts_url": testWitnessURL, "entry_hash": hex.EncodeToString(entry),
		"receipt_b64": base64.StdEncoding.EncodeToString(raw), "leaf_index": 0, "tree_size": 1,
	}
}

// A responder seals a statement; its bundle carries the checkpoint with a
// witness receipt; a requester verifies the bundle, reads the statement and
// shows its own absence beside it.
func TestUnavailableWindowEndToEnd(t *testing.T) {
	ctx := context.Background()
	rf := newFixture(t, "responder", 31)
	responder := rf.open(t)
	witnessKey, directory := testWitness()
	statementRecord, err := responder.SealUnavailableWindow(ctx, rf.clock.t.Add(-time.Hour), rf.clock.t.Add(30*time.Minute), "flood")
	if err != nil {
		t.Fatal(err)
	}
	responderKey := KeyID(responder.signer)

	qf := newFixture(t, "requester", 32)
	requester := qf.open(t)
	qf.clock.t = rf.clock.t.Add(-30 * time.Minute)
	sent, err := requester.Request(ctx, EvidenceRequest{Subject: Subject{Kind: SubjectFullHistory}, Coverage: fresh()}, "responder")
	if err != nil {
		t.Fatal(err)
	}
	closed := qf.clock.t.Add(10 * time.Minute)
	qf.clock.t = closed.Add(time.Minute)
	absence, err := requester.RecordAbsence(ctx, sent.RecordID, closed, "")
	if err != nil {
		t.Fatal(err)
	}

	read := func(t *testing.T, receiptAt time.Time, rows []WitnessRow, responderIsWitness bool) []evidencerequest.WindowStatement {
		t.Helper()
		bundle, err := responder.Bundle(ctx, BundleRequest{Root: statementRecord.RecordID, Payloads: PayloadsSelected})
		if err != nil {
			t.Fatal(err)
		}
		var wire map[string]any
		if err := json.Unmarshal(bundle.JSON, &wire); err != nil {
			t.Fatal(err)
		}
		member := wire["checkpoint"].(map[string]any)
		statement, err := base64.RawURLEncoding.DecodeString(member["cose"].(string))
		if err != nil {
			t.Fatal(err)
		}
		key := witnessKey
		if responderIsWitness {
			key = ed25519.NewKeyFromSeed(seededKey(31)[:32])
		}
		member["witnesses"] = []any{mintReceiptAt(t, statement, key, receiptAt)}
		data, err := json.Marshal(wire)
		if err != nil {
			t.Fatal(err)
		}
		verified, err := VerifyBundleAnswer(data, withWitnesses(responderKeys(responder, 31), rows))
		if err != nil {
			t.Fatal(err)
		}
		statements, err := verified.UnavailableWindowStatements(responderKey, rows)
		if err != nil {
			t.Fatal(err)
		}
		return statements
	}

	t.Run("registered before the window closed", func(t *testing.T) {
		statements := read(t, closed.Add(-time.Minute), directory, false)
		got, err := requester.ShowAbsence(ctx, absence.RecordID, responderKey, statements)
		if err != nil {
			t.Fatal(err)
		}
		label := got.Labels[statementRecord.RecordID]
		if got.ShownAs != evidencerequest.UnavailableShownAs || len(got.CoveredBy) != 1 || label.ReasonClass != "flood" || label.StatedAfterwards {
			t.Fatalf("%+v %+v", got, label)
		}
	})
	t.Run("registered after the window closed is stated afterwards", func(t *testing.T) {
		got, err := requester.ShowAbsence(ctx, absence.RecordID, responderKey, read(t, closed.Add(time.Hour), directory, false))
		if err != nil {
			t.Fatal(err)
		}
		if label := got.Labels[statementRecord.RecordID]; got.ShownAs != evidencerequest.UnavailableShownAs || !label.StatedAfterwards {
			t.Fatalf("%+v %+v", got, label)
		}
	})
	t.Run("a witness the reader does not list registers nothing", func(t *testing.T) {
		other := []WitnessRow{{Name: "another witness", Endpoint: "https://other.test", KeyIDs: directory[0].KeyIDs}}
		bundle, err := responder.Bundle(ctx, BundleRequest{Root: statementRecord.RecordID, Payloads: PayloadsSelected})
		if err != nil {
			t.Fatal(err)
		}
		verified, err := VerifyBundle(bundle.JSON)
		if err != nil {
			t.Fatal(err)
		}
		// No receipt carried: the statement is read but unregistered.
		statements, err := verified.UnavailableWindowStatements(responderKey, other)
		if err != nil {
			t.Fatal(err)
		}
		got, err := requester.ShowAbsence(ctx, absence.RecordID, responderKey, statements)
		if err != nil {
			t.Fatal(err)
		}
		if got.ShownAs != evidencerequest.StateRecordedAbsence || len(got.CoveredBy) != 0 {
			t.Fatalf("an unregistered statement covered the absence: %+v", got)
		}
	})
	t.Run("a receipt under the responder's own key is not independent", func(t *testing.T) {
		own := []WitnessRow{{Name: "responder", Endpoint: testWitnessURL, KeyIDs: []string{responderKey}}}
		bundle, err := responder.Bundle(ctx, BundleRequest{Root: statementRecord.RecordID, Payloads: PayloadsSelected})
		if err != nil {
			t.Fatal(err)
		}
		var wire map[string]any
		if err := json.Unmarshal(bundle.JSON, &wire); err != nil {
			t.Fatal(err)
		}
		member := wire["checkpoint"].(map[string]any)
		statement, _ := base64.RawURLEncoding.DecodeString(member["cose"].(string))
		member["witnesses"] = []any{mintReceiptAt(t, statement, ed25519.NewKeyFromSeed(seededKey(31)[:32]), closed)}
		data, _ := json.Marshal(wire)
		verified, err := VerifyBundleAnswer(data, withWitnesses(responderKeys(responder, 31), own))
		if err != nil {
			t.Fatal(err)
		}
		statements, err := verified.UnavailableWindowStatements(responderKey, own)
		if err != nil {
			t.Fatal(err)
		}
		got, err := requester.ShowAbsence(ctx, absence.RecordID, responderKey, statements)
		if err != nil {
			t.Fatal(err)
		}
		if len(got.CoveredBy) != 0 {
			t.Fatalf("a receipt made under the responder's own key registered a statement: %+v", got)
		}
	})
}

func withWitnesses(keys ResponderKeys, rows []WitnessRow) ResponderKeys {
	keys.Witnesses = rows
	return keys
}
