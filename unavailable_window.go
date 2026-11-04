// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/action-state-group/agent-action-capsule/go/evidencerequest"
	aacwitness "github.com/action-state-group/agent-action-capsule/go/witness"
)

// RecordTypeUnavailableWindow is the record type of an unavailable-window
// statement this book has sealed (ER -02 §4.7).
const RecordTypeUnavailableWindow = "unavailable_window"

// UnavailableWindowText is what a person is shown for an absence an
// unavailable-window statement covers (Sherlock2's wording, evidencebook#223).
// It reads as the holder's statement, never as proof of anything; the class,
// the stated period and the "stated afterwards" label go beside it. The token
// evidencerequest.UnavailableShownAs is the machine form.
const UnavailableWindowText = "Not answered. The holder says these records are unavailable for this period."

// SealUnavailableWindow seals the book's signed statement that it was
// unavailable from start to end, for one of the reason classes
// evidencerequest.UnavailableReasonClasses names (ER -02 §4.7). The statement
// carries the period, the class and the book's signature over them (the
// vectors' profile: Ed25519 over the JCS of end, reason_class and start,
// with key_id the book's signing key).
//
// Sealing is not registering. A reader counts the statement only once a
// checkpoint covering it carries a receipt, with an attested time, from a
// transparency service the reader accepts as independent of this book's
// operator; the host's witness delivery obtains that receipt for the
// checkpoint that covers the returned record, as it does for any other.
func (b *Book) SealUnavailableWindow(ctx context.Context, start, end time.Time, reasonClass string) (Record, error) {
	if !slices.Contains(evidencerequest.UnavailableReasonClasses, reasonClass) {
		return Record{}, fmt.Errorf("%w: reason_class must be one of %v", ErrInvalid, evidencerequest.UnavailableReasonClasses)
	}
	if end.Before(start) {
		return Record{}, fmt.Errorf("%w: an unavailable window must not end before it starts", ErrInvalid)
	}
	members := map[string]any{
		"start": evidencerequest.ProfileTime(start), "end": evidencerequest.ProfileTime(end), "reason_class": reasonClass,
	}
	body, err := evidencerequest.EncodeJCS(members)
	if err != nil {
		return Record{}, err
	}
	sig, err := b.signer.Sign(ctx, body)
	if err != nil {
		return Record{}, err
	}
	members["key_id"] = KeyID(b.signer)
	members["sig"] = hex.EncodeToString(sig)
	statement, err := evidencerequest.EncodeJCS(members)
	if err != nil {
		return Record{}, err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.appendLocked(ctx, Entry{RecordType: RecordTypeUnavailableWindow, EpistemicType: ProducerClaim, Links: []Link{}, Statement: json.RawMessage(statement)})
}

// UnavailableWindowStatements reads the unavailable-window statements out of
// a bundle VerifyBundleAnswer accepted, as the reader holding them for the
// responder whose signing key is responderKey: each record of type
// RecordTypeUnavailableWindow whose header the bundle verified and whose
// position the authenticated anchor covers. The anchor's receipts are
// checked against witnesses, the reader's own list of the transparency
// services it accepts (the directory VerifyBundleAnswer takes); a receipt counts when it verifies there, attests a
// time, and is not made under responderKey (a service the responder
// controls is never independent). The IDs are the records' ids.
//
// A statement that is malformed, unsigned by the responder or unregistered
// is returned all the same: evidencerequest.DisplayAbsence ignores it, as
// the draft's reader does.
func (v VerifiedBundle) UnavailableWindowStatements(responderKey string, witnesses []WitnessRow) ([]evidencerequest.WindowStatement, error) {
	var statements []evidencerequest.WindowStatement
	var receipts []evidencerequest.RegistrationReceipt
	receiptsRead := false
	for _, record := range v.Records {
		if record.Header == nil || !record.HeaderVerified || record.Header.RecordType != RecordTypeUnavailableWindow || !v.Covers(record.Seq) {
			continue
		}
		if !receiptsRead {
			var err error
			if receipts, err = v.registrationReceipts(responderKey, witnesses); err != nil {
				return nil, err
			}
			receiptsRead = true
		}
		decoded, err := evidencerequest.DecodeJSON(record.Header.Statement)
		fields, ok := decoded.(map[string]any)
		if err != nil || !ok {
			// Not a JSON object: a statement that is malformed, which a
			// reader treats as absent.
			continue
		}
		statements = append(statements, evidencerequest.WindowStatement{ID: record.RecordID, Statement: fields, Receipts: receipts})
	}
	return statements, nil
}

// registrationReceipts reads the receipts the bundle's anchor carries, with
// the time each attests, under directory.
func (v VerifiedBundle) registrationReceipts(responderKey string, witnesses []WitnessRow) ([]evidencerequest.RegistrationReceipt, error) {
	if len(v.Witnesses) == 0 {
		return nil, nil
	}
	// A row with no binding is cll, as the directory format has it; a
	// present empty binding would name no binding at all.
	rows := make([]any, 0, len(witnesses))
	for _, w := range witnesses {
		row := map[string]any{"name": w.Name, "endpoint": w.Endpoint, "key_ids": stringsAsAny(w.KeyIDs), "public_keys": stringsAsAny(w.PublicKeys)}
		if w.Binding != "" {
			row["binding"] = w.Binding
		}
		rows = append(rows, row)
	}
	parsed := aacwitness.DirectoryFromValue(rows)
	signed, err := aacwitness.ParseSignedCheckpoint(v.AnchorStatement)
	if err != nil {
		return nil, fmt.Errorf("%w: the anchor does not verify: %v", ErrInvalid, err)
	}
	var out []evidencerequest.RegistrationReceipt
	for i, raw := range v.Witnesses {
		record, err := receiptRecord(raw)
		if err != nil {
			return nil, fmt.Errorf("%w: witness receipt %d: %v", ErrInvalid, i, err)
		}
		_, _, results := aacwitness.VerifyReceipts([]any{record}, &signed, v.AnchorStatement, parsed)
		if len(results) != 1 {
			continue
		}
		result := results[0]
		if result.Status != aacwitness.StatusPass || result.RegistrationTime == nil {
			continue
		}
		independent := result.KeyID == nil || *result.KeyID != responderKey
		out = append(out, evidencerequest.RegistrationReceipt{Time: evidencerequest.ProfileTime(time.Unix(*result.RegistrationTime, 0).UTC()), Accepted: independent})
	}
	return out, nil
}

// receiptRecord decodes a carried receipt with json.Number numbers, as the
// receipt verifier reads one.
func receiptRecord(raw json.RawMessage) (map[string]any, error) {
	entry, err := decodeJSON(raw)
	if err != nil {
		return nil, err
	}
	record, ok := entry.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("not an object")
	}
	return record, nil
}

// ShowAbsence shows the recorded absence absenceRecordID (RecordAbsence)
// beside statements held for the responder whose signing key is
// responderKey (VerifiedBundle.UnavailableWindowStatements), as ER -02 §4.7
// asks: a covered absence is shown as evidencerequest.UnavailableShownAs,
// with each covering statement's class, stated period and registration time
// and whether it was stated after the waiting window closed; an uncovered one
// as recorded_absence. It is never a refusal and never proof the responder
// was attacked, flooded or unable to answer. The absence record and any
// retention commitment it cites are not touched.
func (b *Book) ShowAbsence(_ context.Context, absenceRecordID, responderKey string, statements []evidencerequest.WindowStatement) (evidencerequest.AbsenceDisplay, error) {
	b.mu.Lock()
	record, ok := b.records[absenceRecordID]
	b.mu.Unlock()
	if !ok || record.Header.RecordType != RecordTypeAbsence {
		return evidencerequest.AbsenceDisplay{}, fmt.Errorf("%w: %s is not an absence this book recorded", ErrNotFound, absenceRecordID)
	}
	var absence AbsenceRecord
	if err := json.Unmarshal(record.Header.Statement, &absence); err != nil {
		return evidencerequest.AbsenceDisplay{}, fmt.Errorf("%w: absence record %s: %v", ErrCorrupt, absenceRecordID, err)
	}
	if _, err := hex.DecodeString(responderKey); err != nil || len(responderKey) != 2*ed25519.PublicKeySize {
		return evidencerequest.AbsenceDisplay{}, fmt.Errorf("%w: responder key must be a raw Ed25519 key in lowercase hex", ErrInvalid)
	}
	return evidencerequest.DisplayAbsence(evidencerequest.StateRecordedAbsence, absence.Window.Opened, absence.Window.Closed, statements, responderKey)
}

func stringsAsAny(in []string) []any {
	out := make([]any, len(in))
	for i, s := range in {
		out[i] = s
	}
	return out
}
