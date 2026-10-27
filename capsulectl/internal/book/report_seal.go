package book

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	aacbundle "github.com/action-state-group/agent-action-capsule/go/bundle"
	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook"
	"github.com/spf13/cobra"
)

// recordTypePageSeal is the record `report build --seal` appends: the book's
// own statement of the bytes of a report page it rendered. record_type is
// open vocabulary, and the statement carries exactly the page digest, the
// digest of the Evidence Bundle the page embeds and the Result root the
// bundle is rooted at, so the record names no publisher, registry or trust
// label; the record's subject_ref is the bundle digest, which is how the
// check finds every page sealed for one bundle.
const recordTypePageSeal = "report_page_seal"

type pageSealStatement struct {
	PageDigest   string `json:"page_digest"`
	BundleDigest string `json:"bundle_digest"`
	ResultRoot   string `json:"result_root"`
}

// pageDigest is the SHA-256 of the exact bytes of a page file.
func pageDigest(page []byte) string {
	sum := sha256.Sum256(page)
	return "sha256:" + hex.EncodeToString(sum[:])
}

// sealedPage reports the record sealing a page.
type sealedPage struct {
	RecordID   string `json:"record_id"`
	Seq        uint64 `json:"seq"`
	Checkpoint uint64 `json:"checkpoint,omitempty"`
	// AlreadySealed is set when the book already held a record for these
	// exact page bytes: nothing was appended.
	AlreadySealed bool `json:"already_sealed,omitempty"`
}

// sealPage appends the page's seal to the book and makes sure a checkpoint
// covers it, as SealResult does for a Result. A page whose bytes the book
// already sealed for this bundle finds that record.
func sealPage(ctx context.Context, book *evidencebook.Book, st pageSealStatement) (sealedPage, error) {
	body, err := json.Marshal(st)
	if err != nil {
		return sealedPage{}, err
	}
	prior, err := pageSeals(ctx, book, st.BundleDigest)
	if err != nil {
		return sealedPage{}, err
	}
	var record evidencebook.Record
	var out sealedPage
	for _, p := range prior {
		if p.statement == st {
			record, out.AlreadySealed = p.record, true
			break
		}
	}
	if !out.AlreadySealed {
		record, err = book.Append(ctx, evidencebook.Entry{
			RecordType: recordTypePageSeal, EpistemicType: evidencebook.ProducerClaim,
			SubjectRef: st.BundleDigest, Statement: body,
		})
		if err != nil {
			if record.RecordID != "" {
				return sealedPage{RecordID: record.RecordID, Seq: record.Seq}, err
			}
			return sealedPage{}, bookError(err)
		}
	}
	out.RecordID, out.Seq = record.RecordID, record.Seq
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
	records, err := book.Query(ctx, evidencebook.Filter{RecordType: recordTypePageSeal, SubjectRef: bundleDigest})
	if err != nil {
		return nil, err
	}
	var seals []pageSeal
	for _, r := range records {
		var st pageSealStatement
		if err := json.Unmarshal(r.Header.Statement, &st); err != nil {
			return nil, fmt.Errorf("record %s is a %s whose statement does not decode: %w", r.RecordID, recordTypePageSeal, err)
		}
		seals = append(seals, pageSeal{r, st})
	}
	return seals, nil
}

// The outcomes of checking a page against the book's seals.
const (
	pageMatch    = "match"
	pageMismatch = "mismatch"
	// pageNoSeal: the book holds no seal for the page's bundle.
	pageNoSeal = "no sealed record"
	// pageNotCovered: the book sealed these page bytes, but no checkpoint
	// covers the record yet.
	pageNotCovered = "not covered by a checkpoint"
)

type reportCheckResult struct {
	Page         string `json:"page"`
	PageDigest   string `json:"page_digest"`
	BundleDigest string `json:"bundle_digest"`
	RecordID     string `json:"record_id,omitempty"`
	// Verification is the embedded evidence's own check (the one report
	// build applies), kept apart from Page, which says only whether the
	// page's bytes are the bytes the book sealed.
	Verification string `json:"verification"`
}

// embeddedPageBundle is the Evidence Bundle a report page embeds: the
// emitter writes `window.__BUNDLE__ = <json>;` with every "<" escaped, so
// the first ";</script>" after it ends the JSON.
func embeddedPageBundle(page []byte) (map[string]interface{}, error) {
	const start, end = "window.__BUNDLE__ = ", ";</script>"
	i := bytes.Index(page, []byte(start))
	if i < 0 || !bytes.HasPrefix(bytes.TrimSpace(page), []byte("<!")) {
		return nil, cli.InputError("--page is not a report page: it embeds no Evidence Bundle")
	}
	rest := page[i+len(start):]
	j := bytes.Index(rest, []byte(end))
	if j < 0 {
		return nil, cli.InputError("--page is not a report page: its embedded Evidence Bundle is not terminated")
	}
	value, err := cli.DecodeBundleJSON(rest[:j])
	if err != nil {
		return nil, cli.Hint(cli.ErrInput, "--page's embedded Evidence Bundle does not decode: "+cli.SafeError(err))
	}
	return value, nil
}

// checkPage compares a page with the seals the book holds for its bundle.
func checkPage(ctx context.Context, book *evidencebook.Book, digest string, seals []pageSeal) (outcome, recordID string, err error) {
	if len(seals) == 0 {
		return pageNoSeal, "", nil
	}
	outcome = pageMismatch
	for _, s := range seals {
		if s.statement.PageDigest != digest {
			continue
		}
		covered, err := book.CheckpointCovers(ctx, s.record.Seq)
		if err != nil {
			return "", "", err
		}
		if covered {
			return pageMatch, s.record.RecordID, nil
		}
		outcome, recordID = pageNotCovered, s.record.RecordID
	}
	return outcome, recordID, nil
}

// reportCheckCommand is `report check`: whether a page file is the page the
// book sealed. The output always says both answers. The exit code is
// `verify --bundle`'s (1 INVALID, 3 INCOMPLETE) when the embedded evidence is
// not VALID, which comes first; else 0 for a match, 5 (conflict) for a
// mismatch, and 3 (partial) when the book holds no seal, or one no checkpoint
// covers yet. A file that embeds no bundle is not a page: exit 2.
func reportCheckCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "check", Short: "Check a report page's bytes against the seal its book holds; the embedded evidence is verified separately", Args: cli.NoArgs, RunE: func(c *cobra.Command, _ []string) (err error) {
		p, e := cli.Selected(c)
		if e != nil {
			return e
		}
		path, _ := c.Flags().GetString("page")
		page, e := cli.ReadBundleInput(path)
		if e != nil {
			return e
		}
		value, e := embeddedPageBundle(page)
		if e != nil {
			return e
		}
		bundleDigest, e := aacbundle.BundleDigest(value)
		if e != nil {
			return cli.Hint(cli.ErrInput, "--page's embedded Evidence Bundle has no digest: "+cli.SafeError(e))
		}
		verdict, evidenceErr := judgeEmbedded(value)
		result := reportCheckResult{PageDigest: pageDigest(page), BundleDigest: bundleDigest, Verification: verdict}
		opened, e := openBook(c.Context(), p, false)
		if e != nil {
			return e
		}
		defer func() { err = errors.Join(err, opened.release()) }()
		seals, e := pageSeals(c.Context(), opened.book, bundleDigest)
		if e != nil {
			return e
		}
		if result.Page, result.RecordID, e = checkPage(c.Context(), opened.book, result.PageDigest, seals); e != nil {
			return e
		}
		if e = cli.Output(c, result); e != nil {
			return e
		}
		switch {
		case evidenceErr != nil:
			return evidenceErr
		case result.Page == pageMismatch:
			return errors.Join(cli.ErrConflict, errors.New("the page's bytes are not the bytes the book sealed for its bundle"))
		case result.Page != pageMatch:
			return errors.Join(cli.ErrPartial, errors.New("page: "+result.Page))
		}
		return nil
	}}
	cmd.Flags().String("page", "", "The report page file to check")
	_ = cmd.MarkFlagRequired("page")
	return cmd
}

// sealReport opens the profile's book and seals a written page.
func sealReport(ctx context.Context, p cli.Profile, st pageSealStatement) (sealed sealedPage, err error) {
	opened, err := openBook(ctx, p, false)
	if err != nil {
		return sealedPage{}, err
	}
	defer func() { err = errors.Join(err, opened.release()) }()
	return sealPage(ctx, opened.book, st)
}

// judgeEmbedded is the check `report build` gives a bundle before it writes
// a page: `verify --bundle`'s own verdict, and the page gate over it. The
// gate also refuses a VALID bundle whose report would show fewer rows than
// it has, which is not a verdict on the evidence: the verdict stays VALID and
// the error carries the refusal, with the exit code the gate gives it.
func judgeEmbedded(value map[string]interface{}) (verdict string, gate error) {
	verdict = "INVALID"
	if reread, err := json.Marshal(value); err == nil {
		if judged, err := cli.DecodeBundleJSON(reread); err == nil {
			verdict, _ = cli.BundleVerdict(judged, nil)
		}
	}
	return verdict, cli.PageGate(value)
}
