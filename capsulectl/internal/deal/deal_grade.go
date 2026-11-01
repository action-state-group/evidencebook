package deal

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
	"time"

	aacbundle "github.com/action-state-group/agent-action-capsule/go/bundle"
	"github.com/action-state-group/capsule-cli/cli"
)

// A deal's grade, as the user's own report states it (evidencebook-deal#64,
// actionstate-muse#311 §3.3): the rung of the grade ladder the deal reached,
// where its witnessing stands with the witnessed checkpoint's time, and the
// countersignatures kept with the exact bundles they cover. It is what
// capsulectl checked when the report was made, carried beside the bundle and
// never sealed in it, like the countersign view: the words are the reader's.

// deal_countersignatures keeps every countersigned bundle `deal countersign`
// verified, exactly as verified, keyed by the digest its countersignatures
// sign (aacbundle.BundleDigest, which leaves the countersignatures out). Like
// deal_deferred_emails, it is local only: a countersignature is never
// attached to, or claimed for, any other bundle, and a later report states
// it against the stored bundle it covers.
var dealCountersignaturesSchema = `CREATE TABLE IF NOT EXISTS deal_countersignatures (
	deal_id TEXT NOT NULL,
	bundle_digest TEXT NOT NULL,
	bundle BLOB NOT NULL,
	stored_at TEXT NOT NULL,
	PRIMARY KEY (deal_id, bundle_digest)
)`

// dealGrade is the report's grade block. Rung is the highest rung reached:
// the countersign rung when a stored countersignature resolves, else the
// witness's.
type dealGrade struct {
	Rung        string               `json:"rung"`
	Witness     dealGradeWitness     `json:"witness"`
	Countersign dealGradeCountersign `json:"countersign"`
}

// dealGradeWitness is where the deal's witnessing stands: State is
// not_configured, scheduled, pending or witnessed (as the assurance line
// says), or unchecked when a read that writes nothing found no checkpoint
// covering the deal's last step. A witnessed deal names its witness, At (the
// time of the checkpoint the witness signed) and, witnessed in part, how many
// of its steps the receipt covers.
type dealGradeWitness struct {
	State          string `json:"state"`
	Witness        string `json:"witness,omitempty"`
	At             string `json:"at,omitempty"`
	StepsWitnessed uint64 `json:"steps_witnessed,omitempty"`
	Steps          uint64 `json:"steps,omitempty"`
}

// dealGradeCountersign is the countersign stamp from the stored
// countersignatures: Rung countersigned when one resolves against the
// profile's directory (countersigned_in_part when the deal has steps after
// every resolved one's bundle), else Finding names why none counts
// (not_countersigned, self_countersigned, unresolved_signer, unverified,
// unchecked: the strongest entry decides, as dealCountersignVerify ranks
// them).
type dealGradeCountersign struct {
	Rung string `json:"rung,omitempty"`
	// With a rung, the entry that decides the stamp: the resolved one that
	// reaches furthest into the deal, the latest recomputed_at among those.
	Signer       string `json:"signer,omitempty"`
	RecomputedAt string `json:"recomputed_at,omitempty"`
	ThroughStep  int    `json:"through_step,omitempty"`
	Steps        int    `json:"steps,omitempty"`

	Finding   string                       `json:"finding,omitempty"`
	Directory string                       `json:"directory,omitempty"`
	Entries   []dealStoredCountersignature `json:"entries,omitempty"`
}

// dealStoredCountersignature is one stored countersignature, verified
// against the stored bundle it covers: that bundle's digest, and how far
// into the deal it reaches (through step ThroughStep of Steps). State is
// capsule-cli's (resolved, not_independent, unresolved_signer, unverified,
// invalid), or unchecked when it could not be checked here, UncheckedReason
// saying why: no_directory (the profile names none, or names a file by a
// relative path), directory_unreadable, or bundle_does_not_check (the stored
// bundle no longer checks as this deal's).
type dealStoredCountersignature struct {
	State           string `json:"state"`
	UncheckedReason string `json:"unchecked_reason,omitempty"`
	Signer          string `json:"signer,omitempty"`
	KeyID           string `json:"key_id,omitempty"`
	RecomputedAt    string `json:"recomputed_at,omitempty"`
	Receipt         string `json:"receipt,omitempty"`
	ThroughStep     int    `json:"through_step,omitempty"`
	Steps           int    `json:"steps"`
	BundleDigest    string `json:"bundle_digest"`
}

// profileCountersignDirectory is the countersigner directory the profile
// names, or "" for none. A file named by a relative path (written by hand:
// capsulectl stores an absolute one) would resolve against wherever the
// command runs, so it names none.
func profileCountersignDirectory(p cli.Profile) string {
	d := p.Countersign.Directory
	if strings.Contains(d, "://") || filepath.IsAbs(d) {
		return d
	}
	return ""
}

// laterTime reports whether RFC 3339 time a is after b; a time that does not
// parse is never later.
func laterTime(a, b string) bool {
	ta, err := time.Parse(time.RFC3339Nano, a)
	if err != nil {
		return false
	}
	tb, err := time.Parse(time.RFC3339Nano, b)
	return err != nil || ta.After(tb)
}

// dealWitnessGrade is the witness part of a report bundle's grade: the rung
// it reached and where its witnessing stands, from the same reading the
// assurance line makes (dealAssuranceRung).
func dealWitnessGrade(b map[string]interface{}) (string, dealGradeWitness) {
	a := dealAssuranceRung(b)
	rung, _ := a["rung"].(string)
	w := dealGradeWitness{}
	w.State, _ = a["witness_state"].(string)
	if w.State != "witnessed" {
		return rung, w
	}
	w.Witness, _ = a["witness"].(string)
	// The witnessed checkpoint's time: the cadence tick's checkpoint, or,
	// for a receipt on the deal's own checkpoint, that checkpoint's time.
	if w.At, _ = a["checkpoint_at"].(string); w.At == "" {
		if at, err := bundleCheckpointTime(b); err == nil {
			w.At = at.Format(time.RFC3339)
		}
	}
	w.StepsWitnessed, _ = a["steps_witnessed"].(uint64)
	w.Steps, _ = a["steps"].(uint64)
	return rung, w
}

// dealGradeOf is the grade of the deal, read from its report bundle (nil
// when no bundle could be made without writing: its witness is then
// unchecked) and its stored countersignatures.
func (s *dealSession) dealGradeOf(ctx context.Context, b map[string]interface{}, events []sealedEvent) (dealGrade, error) {
	g := dealGrade{Rung: "self_attested", Witness: dealGradeWitness{State: "unchecked"}}
	if b != nil {
		g.Rung, g.Witness = dealWitnessGrade(b)
	}
	var err error
	if g.Countersign, err = s.storedCountersign(ctx, events); err != nil {
		return dealGrade{}, err
	}
	if g.Countersign.Rung != "" {
		g.Rung = g.Countersign.Rung
	}
	return g, nil
}

// storeCountersigned keeps a countersigned bundle of this deal, as verified,
// with every countersignature an earlier store of the same bundle kept.
func (s *dealSession) storeCountersigned(ctx context.Context, dealID string, b map[string]interface{}) error {
	digest, err := aacbundle.BundleDigest(b)
	if err != nil {
		return err
	}
	var held []byte
	err = s.db.QueryRowContext(ctx, `SELECT bundle FROM deal_countersignatures WHERE deal_id=? AND bundle_digest=?`, dealID, digest).Scan(&held)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	entries, _ := b["countersignatures"].([]interface{})
	if held != nil {
		earlier, err := cli.DecodeBundleJSON(held)
		if err != nil {
			return err
		}
		// An entry is the same entry when its signature is: the bundle file
		// may carry some of the kept ones, all of them, or none.
		seen := map[string]bool{}
		for _, e := range entries {
			m, _ := e.(map[string]interface{})
			seen[fmt.Sprint(m["signature"])] = true
		}
		previous, _ := earlier["countersignatures"].([]interface{})
		for _, e := range previous {
			m, _ := e.(map[string]interface{})
			if !seen[fmt.Sprint(m["signature"])] {
				entries = append(entries, e)
			}
		}
	}
	kept := make(map[string]interface{}, len(b))
	for k, v := range b {
		kept[k] = v
	}
	kept["countersignatures"] = entries
	raw, err := json.Marshal(kept)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO deal_countersignatures (deal_id, bundle_digest, bundle, stored_at) VALUES (?,?,?,?)
ON CONFLICT(deal_id, bundle_digest) DO UPDATE SET bundle=excluded.bundle`,
		dealID, digest, raw, cli.DealClock().UTC().Format(time.RFC3339))
	return err
}

// storedCountersign states every stored countersignature of the deal,
// verified against the stored bundle it covers and resolved against the
// profile's countersigner directory only, so the rung never depends on who
// asks. Nothing is attached to any other bundle.
func (s *dealSession) storedCountersign(ctx context.Context, events []sealedEvent) (dealGradeCountersign, error) {
	out := dealGradeCountersign{Directory: profileCountersignDirectory(s.p)}
	dealID := events[0].Event.DealID
	rows, err := s.db.QueryContext(ctx, `SELECT bundle_digest, bundle FROM deal_countersignatures WHERE deal_id=? ORDER BY stored_at, bundle_digest`, dealID)
	if err != nil {
		// A store no writer has opened since this build has no such table,
		// and so nothing stored.
		if s.shared && strings.Contains(err.Error(), "no such table") {
			out.Finding = "not_countersigned"
			return out, nil
		}
		return out, err
	}
	type stored struct {
		digest string
		raw    []byte
	}
	var held []stored
	for rows.Next() {
		var r stored
		if err = rows.Scan(&r.digest, &r.raw); err != nil {
			return out, errors.Join(err, rows.Close())
		}
		held = append(held, r)
	}
	if err = errors.Join(rows.Err(), rows.Close()); err != nil {
		return out, err
	}
	trusted, err := cli.ParseKeys(s.p.TrustedKeys)
	if err != nil {
		return out, err
	}
	client := &http.Client{Timeout: 10 * time.Second}
	steps := map[string]int{}
	for i, se := range events {
		steps[se.CapsuleID] = i + 1
	}
	// Once the directory could not be read, it is not read again for the
	// next stored bundle: each attempt can wait out the client's timeout
	// while the store's lock is held.
	unreadable := false
	for _, r := range held {
		b, err := s.dealBundleCheck(r.raw, dealID, events)
		through, reason := 0, ""
		var reports []cli.CountersignatureReport
		switch {
		case err != nil:
			reason = "bundle_does_not_check"
		case out.Directory == "":
			reason = "no_directory"
		case unreadable:
			reason = "directory_unreadable"
		default:
			if _, reports, _, err = cli.VerifyCountersignatures(ctx, client, out.Directory, b, trusted); err != nil {
				unreadable, reason = true, "directory_unreadable"
			}
		}
		if err == nil {
			through = steps[fmt.Sprint(b["root"])]
		}
		if reason != "" {
			// Not checked here: the entries are named, never counted.
			unchecked, _ := cli.DecodeBundleJSON(r.raw)
			entries, _ := unchecked["countersignatures"].([]interface{})
			for _, e := range entries {
				m, _ := e.(map[string]interface{})
				signer, _ := m["signer"].(map[string]interface{})
				keyID, _ := signer["key_id"].(string)
				out.Entries = append(out.Entries, dealStoredCountersignature{State: "unchecked", UncheckedReason: reason, KeyID: keyID, ThroughStep: through, Steps: len(events), BundleDigest: r.digest})
			}
			continue
		}
		for _, rep := range reports {
			out.Entries = append(out.Entries, dealStoredCountersignature{
				State: rep.State, Signer: rep.SignerName, KeyID: rep.Signer.KeyID, RecomputedAt: rep.RecomputedAt,
				Receipt: rep.Receipt, ThroughStep: through, Steps: len(events), BundleDigest: r.digest,
			})
		}
	}
	// The strongest entry decides, as capsule-cli ranks a bundle's entries.
	rank := map[string]int{"invalid": 1, "unverified": 1, "unchecked": 2, "unresolved_signer": 3, "not_independent": 4, "resolved": 5}
	finding := map[string]string{"invalid": "unverified", "unverified": "unverified", "unchecked": "unchecked", "unresolved_signer": "unresolved_signer", "not_independent": "self_countersigned"}
	best := ""
	var decides *dealStoredCountersignature
	for i, e := range out.Entries {
		if rank[e.State] > rank[best] {
			best = e.State
		}
		if e.State == "resolved" && (decides == nil || e.ThroughStep > decides.ThroughStep ||
			(e.ThroughStep == decides.ThroughStep && laterTime(e.RecomputedAt, decides.RecomputedAt))) {
			decides = &out.Entries[i]
		}
	}
	switch best {
	case "":
		out.Finding = "not_countersigned"
	case "resolved":
		// Countersigned in part, like witnessed in part, when the deal has
		// steps after every resolved countersignature's bundle.
		out.Rung = "countersigned"
		if decides.ThroughStep < len(events) {
			out.Rung = "countersigned_in_part"
		}
		out.Signer, out.RecomputedAt, out.ThroughStep, out.Steps = decides.Signer, decides.RecomputedAt, decides.ThroughStep, decides.Steps
	default:
		out.Finding = finding[best]
	}
	return out, nil
}
