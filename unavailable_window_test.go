// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/action-state-group/agent-action-capsule/go/evidencerequest"
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

	read := func(t *testing.T, receiptAt time.Time) []evidencerequest.WindowStatement {
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
		member["witnesses"] = []any{mintReceiptAt(t, statement, witnessKey, receiptAt)}
		data, err := json.Marshal(wire)
		if err != nil {
			t.Fatal(err)
		}
		verified, err := VerifyBundleAnswer(data, withWitnesses(responderKeys(responder, 31), directory))
		if err != nil {
			t.Fatal(err)
		}
		statements, err := verified.UnavailableWindowStatements(responderKey, directory)
		if err != nil {
			t.Fatal(err)
		}
		return statements
	}

	t.Run("registered before the window closed", func(t *testing.T) {
		statements := read(t, closed.Add(-time.Minute))
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
		got, err := requester.ShowAbsence(ctx, absence.RecordID, responderKey, read(t, closed.Add(time.Hour)))
		if err != nil {
			t.Fatal(err)
		}
		if label := got.Labels[statementRecord.RecordID]; got.ShownAs != evidencerequest.UnavailableShownAs || !label.StatedAfterwards {
			t.Fatalf("%+v %+v", got, label)
		}
	})
	t.Run("a witness the reader does not list registers nothing", func(t *testing.T) {
		// The bundle's receipt verifies under the directory it was checked
		// with; the reader then reads the statements under a directory that
		// lists another witness.
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
		member["witnesses"] = []any{mintReceiptAt(t, statement, witnessKey, closed.Add(-time.Minute))}
		data, _ := json.Marshal(wire)
		verified, err := VerifyBundleAnswer(data, withWitnesses(responderKeys(responder, 31), directory))
		if err != nil {
			t.Fatal(err)
		}
		other := []WitnessRow{{Name: "another witness", Endpoint: "https://other.test", KeyIDs: directory[0].KeyIDs}}
		statements, err := verified.UnavailableWindowStatements(responderKey, other)
		if err != nil {
			t.Fatal(err)
		}
		got, err := requester.ShowAbsence(ctx, absence.RecordID, responderKey, statements)
		if err != nil {
			t.Fatal(err)
		}
		if got.ShownAs != evidencerequest.StateRecordedAbsence || len(got.CoveredBy) != 0 {
			t.Fatalf("a receipt from an unlisted witness registered a statement: %+v", got)
		}
	})
	t.Run("a bundle with no receipt registers nothing", func(t *testing.T) {
		bundle, err := responder.Bundle(ctx, BundleRequest{Root: statementRecord.RecordID, Payloads: PayloadsSelected})
		if err != nil {
			t.Fatal(err)
		}
		verified, err := VerifyBundle(bundle.JSON)
		if err != nil {
			t.Fatal(err)
		}
		statements, err := verified.UnavailableWindowStatements(responderKey, directory)
		if err != nil {
			t.Fatal(err)
		}
		got, err := requester.ShowAbsence(ctx, absence.RecordID, responderKey, statements)
		if err != nil {
			t.Fatal(err)
		}
		if len(got.CoveredBy) != 0 {
			t.Fatalf("an unregistered statement covered the absence: %+v", got)
		}
	})
	t.Run("a receipt under the responder's own key is not independent", func(t *testing.T) {
		der, err := x509.MarshalPKIXPublicKey(responder.signer.PublicKey())
		if err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256(der)
		// The directory lists the responder's key by the SHA-256 of its DER form.
		own := []WitnessRow{{Name: "responder", Endpoint: testWitnessURL, KeyIDs: []string{hex.EncodeToString(digest[:])}, PublicKeys: []string{base64.StdEncoding.EncodeToString(der)}}}
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

func TestKeyIDNames(t *testing.T) {
	raw := hex.EncodeToString(seededKey(31)[32:])
	der, err := x509.MarshalPKIXPublicKey(ed25519.PublicKey(seededKey(31)[32:]))
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(der)
	for name, id := range map[string]string{"raw hex": raw, "DER digest": hex.EncodeToString(sum[:])} {
		if !keyIDNames(id, raw) {
			t.Fatalf("%s does not name the key", name)
		}
	}
	if keyIDNames(hex.EncodeToString(seededKey(77)[32:]), raw) || keyIDNames("zz", raw) {
		t.Fatal("another key id named the key")
	}
}

func withWitnesses(keys ResponderKeys, rows []WitnessRow) ResponderKeys {
	keys.Witnesses = rows
	return keys
}

// A bundle with the anchor's receipts as given: the statement is covered only
// by a receipt that verifies under the reader's directory, attests a time and
// is not made under the responder's or the checkpoint's own key.
func TestUnavailableWindowReceiptRules(t *testing.T) {
	ctx := context.Background()
	rf := newFixture(t, "responder", 31)
	responder := rf.open(t)
	witnessKey, directory := testWitness()
	record, err := responder.SealUnavailableWindow(ctx, rf.clock.t.Add(-time.Hour), rf.clock.t.Add(time.Hour), "maintenance")
	if err != nil {
		t.Fatal(err)
	}
	responderKey := KeyID(responder.signer)
	checkpointKey := responderKeys(responder, 31).Checkpoint
	late, early := rf.clock.t.Add(30*time.Minute), rf.clock.t.Add(10*time.Minute)

	read := func(t *testing.T, rows []WitnessRow, receipts func(statement []byte) []any) []evidencerequest.WindowStatement {
		t.Helper()
		bundle, err := responder.Bundle(ctx, BundleRequest{Root: record.RecordID, Payloads: PayloadsSelected})
		if err != nil {
			t.Fatal(err)
		}
		var wire map[string]any
		if err := json.Unmarshal(bundle.JSON, &wire); err != nil {
			t.Fatal(err)
		}
		member := wire["checkpoint"].(map[string]any)
		statement, _ := base64.RawURLEncoding.DecodeString(member["cose"].(string))
		member["witnesses"] = receipts(statement)
		data, _ := json.Marshal(wire)
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
	registration := func(statements []evidencerequest.WindowStatement) string {
		if len(statements) != 1 {
			t.Fatalf("%d statements", len(statements))
		}
		reading := evidencerequest.RecognizeUnavailableWindow(statements[0].Statement, statements[0].Receipts, responderKey)
		if !reading.Recognized {
			return ""
		}
		return reading.RegistrationTime
	}

	t.Run("a receipt without a time registers nothing", func(t *testing.T) {
		got := read(t, directory, func(s []byte) []any { return []any{mintReceipt(t, s, witnessKey)} })
		if registration(got) != "" {
			t.Fatalf("a receipt attesting no time registered the statement: %+v", got)
		}
	})
	t.Run("the earliest of several receipts is the registration time", func(t *testing.T) {
		got := read(t, directory, func(s []byte) []any {
			return []any{mintReceiptAt(t, s, witnessKey, late), mintReceiptAt(t, s, witnessKey, early)}
		})
		if want := evidencerequest.ProfileTime(early); registration(got) != want {
			t.Fatalf("registration time %q, want %q", registration(got), want)
		}
	})
	t.Run("a receipt under the checkpoint's own key is not independent", func(t *testing.T) {
		own := []WitnessRow{{Name: "log", Endpoint: testWitnessURL, KeyIDs: []string{checkpointKey}}}
		got := read(t, own, func(s []byte) []any {
			return []any{mintReceiptAt(t, s, ed25519.NewKeyFromSeed(seededKey(131)[:32]), early)}
		})
		if registration(got) != "" {
			t.Fatalf("a receipt made under the checkpoint key registered the statement: %+v", got)
		}
	})
}

func TestUnavailableWindowReadRefusals(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, "requester", 30)
	requester := f.open(t)
	sent, err := requester.Request(ctx, EvidenceRequest{Subject: Subject{Kind: SubjectFullHistory}, Coverage: fresh()}, "book-a")
	if err != nil {
		t.Fatal(err)
	}
	f.clock.t = f.clock.t.Add(time.Hour)
	absence, err := requester.RecordAbsence(ctx, sent.RecordID, f.clock.t.Add(-time.Minute), "")
	if err != nil {
		t.Fatal(err)
	}
	good := hex.EncodeToString(seededKey(31)[32:])
	for name, key := range map[string]string{"empty": "", "uppercase": strings.ToUpper(good), "short": good[:62], "not hex": strings.Repeat("zz", 32)} {
		if _, err := requester.ShowAbsence(ctx, absence.RecordID, key, nil); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%s responder key: %v", name, err)
		}
		if _, err := (VerifiedBundle{}).UnavailableWindowStatements(key, nil); !errors.Is(err, ErrInvalid) {
			t.Fatalf("%s responder key on read: %v", name, err)
		}
	}
	// A record that is no absence of this book.
	if _, err := requester.ShowAbsence(ctx, strings.Repeat("0", 64), good, nil); !errors.Is(err, ErrNotFound) {
		t.Fatalf("an unknown record: %v", err)
	}
	// A statement record that is not a JSON object is skipped, not an error.
	notObject := json.RawMessage(`["flood"]`)
	verified := VerifiedBundle{AnchorAuthenticated: true, IntervalFirst: 1, IntervalLast: 1, Records: []PeerRecord{{RecordID: "r", Seq: 1, HeaderVerified: true, Header: &Header{RecordType: RecordTypeUnavailableWindow, Statement: notObject}}}}
	got, err := verified.UnavailableWindowStatements(good, nil)
	if err != nil || len(got) != 0 {
		t.Fatalf("a statement that is not an object: %v %v", got, err)
	}
}
