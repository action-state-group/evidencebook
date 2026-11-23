package deal

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"time"

	"github.com/action-state-group/agent-action-capsule/go/witness"
	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook"
	"github.com/action-state-group/evidencebook/dealprofile/xdealv1"
)

// stampedAtUnknown is what deal receive says of a ruling no receipt from a
// witness the agreement names times: a neutral fact, so a reader can tell
// "could not check" from "checked, no binding" (Sherlock2, #819).
const stampedAtUnknown = "unknown (no receipt from a witness the agreement names)"

// readJSONFiles reads each file a repeatable flag names, each one JSON
// document, as raw bytes.
func readJSONFiles(flag string, paths []string) ([]json.RawMessage, error) {
	out := make([]json.RawMessage, 0, len(paths))
	for _, p := range paths {
		raw, err := os.ReadFile(p)
		if err != nil {
			return nil, cli.InputError("cannot read " + flag + " " + p)
		}
		if !json.Valid(raw) {
			return nil, cli.InputError(flag + " " + p + " is not a JSON document")
		}
		out = append(out, json.RawMessage(raw))
	}
	return out, nil
}

// independence adds to a received ruling's output its stamped time and,
// when SharedOperator gives it, the label "not independent (shared
// operator)" (#780, #819). The stamped time is the earliest registration
// time among the push's witness receipts that verify, for the stamp's shelf
// checkpoint, under a key the holder's agreement names; never the ruling's
// own at. The bindings are the push's and the holder's own (--binding); the
// revocations are only the holder's (--revocation): a revocation's time is
// one key's word, so an adjudicator could backdate one in its own push.
// There is no positive state: with no label, "independence" is left out.
func (s *dealSession) independence(ctx context.Context, info *receivedInfo, push v1Push, own, revocations []json.RawMessage, out map[string]any) error {
	d, err := s.v1DealOf(ctx, info.dealID)
	if err != nil {
		return err
	}
	if d == nil || d.OwnAgreementID == "" {
		return nil
	}
	book, err := s.ownBookV1(ctx, d)
	if err != nil || len(book) == 0 {
		return err
	}
	ag, ok := agreeOf(book[0])
	if !ok {
		return nil
	}
	at := rulingStampedAt(push, ag)
	if at.IsZero() {
		out["stamped_at"] = stampedAtUnknown
	} else {
		out["stamped_at"] = at.Format(time.RFC3339)
	}
	var bindings, revoked [][]byte
	for _, b := range append(append([]json.RawMessage{}, push.Bindings...), own...) {
		bindings = append(bindings, b)
	}
	for _, r := range revocations {
		revoked = append(revoked, r)
	}
	if label := xdealv1.SharedOperator(info.book.Record, *ag, at, bindings, revoked); label != "" {
		out["independence"] = label
	}
	return nil
}

// rulingStampedAt is the earliest registration time among the push's
// receipts that verify for its stamp's shelf checkpoint under a witness key
// the agreement names; zero when there is none.
func rulingStampedAt(push v1Push, ag *xdealv1.AgreeBody) time.Time {
	if push.StampProof == nil || len(ag.Witnesses) == 0 {
		return time.Time{}
	}
	named := make([]string, 0, len(ag.Witnesses))
	for _, w := range ag.Witnesses {
		named = append(named, strings.ToLower(w.KeyID))
	}
	var earliest time.Time
	for _, rc := range push.WitnessReceipts {
		dir, err := receiptDirectory(rc.TSURL, named)
		if err != nil {
			continue
		}
		check, keyID, at, ok, err := evidencebook.VerifyWitnessReceiptTime(push.StampProof.ShelfCheckpoint, rc, dir)
		if err != nil || !ok || check.State != evidencebook.WitnessVerified || !containsString(named, keyID) {
			continue
		}
		if earliest.IsZero() || at.Before(earliest) {
			earliest = at
		}
	}
	return earliest
}

// receiptDirectory is a witness directory of one row: the receipt's witness
// URL with the key ids an agreement names. A receipt verifies under it only
// when one of those keys signed it.
func receiptDirectory(url string, keys []string) (witness.Directory, error) {
	url = strings.TrimRight(url, "/")
	row, err := json.Marshal(map[string]any{
		"witnesses": []map[string]any{{
			"name":        "a witness the agreement names",
			"endpoint":    witness.EndpointOf(url),
			"binding":     witness.BindingOf(url),
			"key_ids":     keys,
			"public_keys": []string{},
		}},
	})
	if err != nil {
		return witness.Directory{}, err
	}
	return witness.ParseDirectory(row)
}
