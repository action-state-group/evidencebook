// SPDX-License-Identifier: Apache-2.0

package report

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"

	aacbundle "github.com/action-state-group/agent-action-capsule/go/bundle"
	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/capsule-viewer/go/presentation"
	"github.com/action-state-group/evidencebook"
)

// RecordTypePageSeal is the record SealPage appends: the book's own
// statement of the bytes of a report page. record_type is open vocabulary,
// and the statement carries exactly the page digest, the digest of the
// Evidence Bundle the page embeds and the Result root the bundle is rooted
// at, so the record names no publisher, registry or trust label; its
// subject_ref is the bundle digest, which is how CheckPage finds every page
// sealed for one bundle.
const RecordTypePageSeal = "report_page_seal"

type pageSealStatement struct {
	PageDigest   string `json:"page_digest"`
	BundleDigest string `json:"bundle_digest"`
	ResultRoot   string `json:"result_root"`
}

// PageDigest is the SHA-256 of the exact bytes of a page file, as a page seal
// records it ("sha256:<hex>").
func PageDigest(page []byte) string {
	sum := sha256.Sum256(page)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// Seal reports the record sealing a page.
type Seal struct {
	RecordID string `json:"record_id"`
	Seq      uint64 `json:"seq"`
	// Checkpoint is the size of the checkpoint cut to cover the record, or
	// 0 when one already covered it.
	Checkpoint uint64 `json:"checkpoint,omitempty"`
	// AlreadySealed is set when the book already held a record for these
	// exact page bytes: nothing was appended.
	AlreadySealed bool `json:"already_sealed,omitempty"`
	// Revision numbers the pages sealed over one Result: this seal's place,
	// from 1, among the book's page seals with the same Result root, in log
	// order. A page that changes only its presentation embeds the same
	// Result, so sealing it bumps the revision and leaves the Result as it
	// was; a repeat of the same page keeps its revision.
	Revision int `json:"revision"`
}

// SealPage appends a RecordTypePageSeal record for page, the exact bytes of
// a report page, to book and makes sure a checkpoint covers it, as
// Book.SealResult does for a Result. Everything the record states is read
// from page itself: its digest, and the digest and Result root of the bundle
// it embeds. A page whose bytes the book already sealed for its bundle finds
// that record and appends nothing. A file that is not a report page, a draft
// page, or a page whose embedded bundle the page gate refuses is refused.
//
// A failure after the record was appended returns the record with the
// error, so the caller never loses its id. It is not atomic: callers that
// may seal the same page concurrently must serialize.
func SealPage(ctx context.Context, book *evidencebook.Book, page []byte) (Seal, error) {
	inspected, err := inspect(page)
	if err != nil {
		return Seal{}, err
	}
	if inspected.Refusal != nil {
		return Seal{}, errors.Join(refusal("the page's embedded bundle is not one a page may be written for, so it is not sealed"), inspected.Refusal)
	}
	if inspected.draft {
		return Seal{}, refusal("a draft page is never sealed")
	}
	st := pageSealStatement{PageDigest: inspected.PageDigest, BundleDigest: inspected.BundleDigest, ResultRoot: inspected.root}
	body, err := json.Marshal(st)
	if err != nil {
		return Seal{}, err
	}
	prior, err := pageSeals(ctx, book, st.BundleDigest)
	if err != nil {
		return Seal{}, err
	}
	var record evidencebook.Record
	var out Seal
	for _, p := range prior {
		if p.statement == st {
			record, out.AlreadySealed = p.record, true
			break
		}
	}
	if !out.AlreadySealed {
		record, err = book.Append(ctx, evidencebook.Entry{
			RecordType: RecordTypePageSeal, EpistemicType: evidencebook.ProducerClaim,
			SubjectRef: st.BundleDigest, Statement: body,
		})
		if err != nil {
			if record.RecordID != "" {
				return Seal{RecordID: record.RecordID, Seq: record.Seq}, err
			}
			return Seal{}, err
		}
	}
	out.RecordID, out.Seq = record.RecordID, record.Seq
	if out.Revision, err = revision(ctx, book, record.RecordID, st.ResultRoot); err != nil {
		return out, err
	}
	covered, err := book.CheckpointCovers(ctx, record.Seq)
	if err != nil {
		return out, err
	}
	if !covered {
		cp, err := book.Checkpoint(ctx)
		if err != nil {
			return out, err
		}
		out.Checkpoint = cp.Entries
	}
	return out, nil
}

type pageSeal struct {
	record    evidencebook.Record
	statement pageSealStatement
}

// pageSeals are the page seals the book holds for one bundle digest.
func pageSeals(ctx context.Context, book *evidencebook.Book, bundleDigest string) ([]pageSeal, error) {
	records, err := book.Query(ctx, evidencebook.Filter{RecordType: RecordTypePageSeal, SubjectRef: bundleDigest})
	if err != nil {
		return nil, err
	}
	var seals []pageSeal
	for _, r := range records {
		var st pageSealStatement
		if err := json.Unmarshal(r.Header.Statement, &st); err != nil {
			return nil, fmt.Errorf("record %s is a %s whose statement does not decode: %w", r.RecordID, RecordTypePageSeal, err)
		}
		seals = append(seals, pageSeal{r, st})
	}
	return seals, nil
}

// revision is the place, from 1, of the page seal recordID among the book's
// page seals over resultRoot, in log order. A seal whose statement does not
// decode states no Result root, so it is no revision of this one and does
// not stop this page's seal.
func revision(ctx context.Context, book *evidencebook.Book, recordID, resultRoot string) (int, error) {
	records, err := book.Query(ctx, evidencebook.Filter{RecordType: RecordTypePageSeal})
	if err != nil {
		return 0, err
	}
	n := 0
	for _, r := range records {
		var st pageSealStatement
		if json.Unmarshal(r.Header.Statement, &st) != nil || st.ResultRoot != resultRoot {
			continue
		}
		n++
		if r.RecordID == recordID {
			return n, nil
		}
	}
	return 0, fmt.Errorf("page seal %s is not among the book's page seals over Result %s", recordID, resultRoot)
}

// The outcomes of checking a page against the book's seals.
const (
	// PageMatch: the book sealed these page bytes, under a checkpoint.
	PageMatch = "match"
	// PageMismatch: the book sealed pages for this bundle, none with these
	// bytes.
	PageMismatch = "mismatch"
	// PageNoSeal: the book holds no seal for the page's bundle.
	PageNoSeal = "no sealed record"
	// PageNotCovered: the book sealed these page bytes, but no checkpoint
	// covers the record yet.
	PageNotCovered = "not covered by a checkpoint"
)

// Check is what CheckPage or InspectPage found. CheckPage gives both
// answers: whether the page's bytes are the bytes the book sealed (Outcome,
// RecordID), and the embedded evidence's own check (Verdict and Refusal),
// which is kept apart. InspectPage gives only the second, and leaves Outcome
// and RecordID empty.
type Check struct {
	PageDigest   string
	BundleDigest string
	Outcome      string
	// RecordID is the seal record a match (or a not-yet-covered match)
	// found.
	RecordID string
	// Verdict is `verify --bundle`'s verdict on the bundle the page embeds.
	Verdict string
	// Refusal is the page gate's refusal of the embedded bundle, nil when
	// Build would render it. A VALID bundle whose report would show fewer
	// rows than it has keeps its verdict and still carries a refusal.
	Refusal error

	root  string
	draft bool
}

// InspectPage reads the Evidence Bundle page embeds and judges it, with no
// book: the Check it returns has every answer but Outcome and RecordID. A
// file that embeds no bundle is refused (ErrRefused).
func InspectPage(page []byte) (Check, error) { return inspect(page) }

func inspect(page []byte) (Check, error) {
	value, err := embeddedBundle(page)
	if err != nil {
		return Check{}, err
	}
	bundleDigest, err := aacbundle.BundleDigest(value)
	if err != nil {
		return Check{}, refusal("the page's embedded Evidence Bundle has no digest: " + cli.SafeError(err))
	}
	out := Check{PageDigest: PageDigest(page), BundleDigest: bundleDigest, Verdict: "INVALID"}
	out.root, _ = value["root"].(string)
	extensions, _ := value["extensions"].(map[string]interface{})
	card, _ := extensions[presentation.CardExtension].(map[string]interface{})
	out.draft = card["draft"] == true
	if reread, err := json.Marshal(value); err == nil {
		if judged, err := cli.DecodeBundleJSON(reread); err == nil {
			out.Verdict, _ = cli.BundleVerdict(judged, nil)
		}
	}
	out.Refusal = cli.PageGate(value)
	return out, nil
}

// CheckPage is InspectPage, then the comparison of the page's bytes with the
// seals book holds for the bundle it embeds.
func CheckPage(ctx context.Context, book *evidencebook.Book, page []byte) (Check, error) {
	out, err := inspect(page)
	if err != nil {
		return Check{}, err
	}
	seals, err := pageSeals(ctx, book, out.BundleDigest)
	if err != nil {
		return Check{}, err
	}
	out.Outcome = PageNoSeal
	if len(seals) > 0 {
		out.Outcome = PageMismatch
	}
	for _, s := range seals {
		if s.statement.PageDigest != out.PageDigest {
			continue
		}
		covered, err := book.CheckpointCovers(ctx, s.record.Seq)
		if err != nil {
			return Check{}, err
		}
		if covered {
			out.Outcome, out.RecordID = PageMatch, s.record.RecordID
			break
		}
		out.Outcome, out.RecordID = PageNotCovered, s.record.RecordID
	}
	return out, nil
}

// bundleElement is the start tag of the JSON data element that carries the
// bundle in a page built by capsule-viewer 0.4.0 or later (capsule-viewer#109).
var bundleElement = regexp.MustCompile(`(?is)<script\b[^>]*\btype\s*=\s*["']?application/json["']?[^>]*>`)

// embeddedBundle is the Evidence Bundle a report page embeds. A page built by
// capsule-viewer 0.4.0 or later carries it as the text of its first
// `<script type="application/json">` element, with every "<" escaped, so the
// first "</script>" after the start tag ends the JSON. A page sealed before
// that wrote `window.__BUNDLE__ = <json>;` and is still read, so its seal
// still checks.
func embeddedBundle(page []byte) (map[string]interface{}, error) {
	if !bytes.HasPrefix(bytes.TrimSpace(page), []byte("<!")) {
		return nil, refusal("not a report page: it embeds no Evidence Bundle")
	}
	if m := bundleElement.FindIndex(page); m != nil {
		rest := page[m[1]:]
		j := bytes.Index(rest, []byte("</script>"))
		if j < 0 {
			return nil, refusal("not a report page: its embedded Evidence Bundle is not terminated")
		}
		return decodeEmbedded(rest[:j])
	}
	const start, end = "window.__BUNDLE__ = ", ";</script>"
	i := bytes.Index(page, []byte(start))
	if i < 0 {
		return nil, refusal("not a report page: it embeds no Evidence Bundle")
	}
	rest := page[i+len(start):]
	j := bytes.Index(rest, []byte(end))
	if j < 0 {
		return nil, refusal("not a report page: its embedded Evidence Bundle is not terminated")
	}
	return decodeEmbedded(rest[:j])
}

func decodeEmbedded(raw []byte) (map[string]interface{}, error) {
	value, err := cli.DecodeBundleJSON(bytes.TrimSpace(raw))
	if err != nil {
		return nil, refusal("the page's embedded Evidence Bundle does not decode: " + cli.SafeError(err))
	}
	return value, nil
}
