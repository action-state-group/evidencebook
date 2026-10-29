// SPDX-License-Identifier: Apache-2.0

// Package report renders a verified Evidence Bundle rooted at a sealed
// Evidence Result v0 into one offline HTML page, and seals that page's
// digest in an evidence book.
//
// Build verifies before it renders: the neutral Evidence Bundle verifier,
// the root gate (the root carries the one Result the page is built on), the
// Result gate, the book's own verifier for a Result in book form, and the
// page gate (`capsulectl verify --bundle` must call the bundle VALID). It
// reads only the bundle it is given and never a claim the bundle does not
// carry. The page is capsule-viewer's emitter shell around the bundle and
// capsule-cli's pinned browser runtime; it loads nothing from the network.
//
// SealPage seals a page's digest in a book; InspectPage judges the evidence
// a page embeds, with no book; and CheckPage also tells whether a page file
// is a page the book sealed. `capsulectl book report build` and `report
// check` are these functions behind flags.
package report

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"sort"
	"strings"

	aacbundle "github.com/action-state-group/agent-action-capsule/go/bundle"
	"github.com/action-state-group/agent-action-capsule/go/disclosure"
	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/capsule-viewer/go/emitter"
	"github.com/action-state-group/capsule-viewer/go/pageruntime"
	"github.com/action-state-group/capsule-viewer/go/presentation"
	"github.com/action-state-group/evidencebook"
)

// ErrRefused is the class of every refusal this package makes about its
// input: a bundle that does not decode or verify, a root that is not one
// Result, a view outside the card set or with a bad presentation block or
// wording pack, a permalink or seal for a page that cannot have one, a page that is not a
// report page.
// The page gate's refusal is not one of them: it carries capsule-cli's own
// class (cli.ErrPartial for INCOMPLETE, cli.ErrBundleInvalid for INVALID).
var ErrRefused = errors.New("report: refused")

// refusal is an ErrRefused whose message is the whole sentence.
type refusal string

func (r refusal) Error() string        { return string(r) }
func (r refusal) Is(target error) bool { return target == ErrRefused }

func refusef(format string, args ...any) error { return refusal(fmt.Sprintf(format, args...)) }

// refusedBy is an ErrRefused that keeps the error it was refused by, and
// that error's message and class.
type refusedBy struct{ err error }

func (r refusedBy) Error() string        { return r.err.Error() }
func (r refusedBy) Unwrap() error        { return r.err }
func (r refusedBy) Is(target error) bool { return target == ErrRefused }

// cards is the closed profile set of the Evidence Contract schema (its
// `profile`-discriminated oneOf; TestCardsAreTheContractProfiles keeps this
// list equal to what the schema declares), sorted.
var cards = []string{"attribution", "human_role", "obligation", "outcome", "process", "quality", "settlement"}

// Cards returns the cards a View may name, sorted: the Evidence Contract's
// profile set. Every card renders the one Result page today; the card is
// recorded in the page's report-card/v1 block for the viewer to key on.
func Cards() []string { return slices.Clone(cards) }

// Presentation is the presentation/v1 header block: exactly what the
// viewer's header reads, and nothing else. Every member is optional, but a
// block names at least one, and a logo is an inline data:image/ URL.
type Presentation struct {
	ProducerDisplayName string `json:"producer_display_name,omitempty"`
	LogoDataURL         string `json:"logo_data_url,omitempty"`
	Title               string `json:"title,omitempty"`
}

// DecodePresentation reads one presentation/v1 block from JSON, refusing
// any member outside the three, and checks it.
func DecodePresentation(raw []byte) (Presentation, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var block Presentation
	if err := decoder.Decode(&block); err != nil {
		return Presentation{}, refusal("presentation must be a JSON object with only producer_display_name, logo_data_url and title (all optional strings)")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return Presentation{}, refusal("presentation input must contain one JSON value")
	}
	return block, block.check()
}

func (p Presentation) check() error {
	if p.LogoDataURL != "" && !strings.HasPrefix(p.LogoDataURL, "data:image/") {
		return refusal("presentation logo_data_url must be an inline data:image/ URL; the report loads nothing from the network")
	}
	if p == (Presentation{}) {
		return refusal("presentation names none of producer_display_name, logo_data_url or title")
	}
	return nil
}

func (p Presentation) block() map[string]interface{} {
	out := make(map[string]interface{})
	if p.ProducerDisplayName != "" {
		out["producer_display_name"] = p.ProducerDisplayName
	}
	if p.LogoDataURL != "" {
		out["logo_data_url"] = p.LogoDataURL
	}
	if p.Title != "" {
		out["title"] = p.Title
	}
	return out
}

// View is how the one Result is shown: the card, an optional header,
// whether the page is a draft, an optional wording pack, and whether the
// page is the contract report. A draft page is marked so in its
// report-card/v1 block, gets no permalink and is never sealed.
type View struct {
	Card         string
	Presentation *Presentation
	Draft        bool
	// Wording is the wording layer over the viewer's own base pack (SDK
	// SURFACE `report`: base plus the pack's): its entries replace the
	// base's for the same keys, and the entries the viewer locks stay
	// locked. A host with words of its own combines them with the pack's
	// into this one pack. The pack's page.title, if any, is the document
	// <title>; Presentation.Title is the header in the page, and both apply.
	// The layer lives in the page, not in the bundle it embeds, so a page
	// built with one gets no permalink: the permalink carries the bundle
	// alone, and the viewer it opens in would show its own words.
	Wording *Wording
	// ContractReport selects the contract report (AAC
	// spec/contract-report-v1.md): the page's bundle carries the empty
	// contract-report/v1 block, which the viewer's contract-report module
	// resolves on. A Result with no grouping data for the card draws the
	// generic Result page there, listing every claim. A bundle whose
	// outcome-report/v1 or eu-ai-act-compliance/v1 block already draws the
	// contract report looks the same; the block records the selection.
	ContractReport bool
}

// Wording is an aac.wording-pack/v0 wording pack as distributed: its exact
// text and the lowercase hex SHA-256 of its UTF-8 bytes (wording_sha256).
// Build checks the one against the other, and the pack's shape.
type Wording struct {
	Pack   string
	SHA256 string
}

// contractReportExtension is the block that opts a Result root into the
// contract report; it carries nothing.
const contractReportExtension = "contract-report/v1"

// The forms a Result root takes (Page.Form).
const (
	// FormPayload: the Result is a disclosed member of the root.
	FormPayload = "payload"
	// FormBook: the root's disclosed agent_input is an evidence_result
	// record header whose statement is the Result.
	FormBook = "book"
)

// The labels of a bundle's verification (Page.Verification).
const (
	VerificationPass = "pass"
	// VerificationClosureWithheld: the bundle declares records of its
	// closure missing; the claims citing them count as unsupported.
	VerificationClosureWithheld = "graph_closure withheld"
)

// Page is a built report: what Build established about the bundle, and the
// page itself. HTML is nil when the bundle verified but the page gate
// refused it; the rest is still filled in.
type Page struct {
	// Root is the Result's record id; Form is FormPayload or FormBook;
	// Member is the disclosed member carrying the Result.
	Root, Form, Member string
	Card               string
	Draft              bool
	// Claims counts the Result's claims; UnsupportedClaims those whose cited
	// evidence is not all in the bundle, which the page shows unsupported,
	// never met.
	Claims, UnsupportedClaims int
	ContractRefs              []string
	// BundleDigest is the digest of the bundle the page embeds, with its
	// card and header blocks.
	BundleDigest string
	// Verification is VerificationPass or VerificationClosureWithheld.
	Verification string
	// Verdict is `verify --bundle`'s verdict on the embedded bundle (VALID,
	// INCOMPLETE or INVALID); UnmetClaims names the claims that kept it
	// from VALID.
	Verdict     string
	UnmetClaims []string
	HTML        []byte

	bundle map[string]interface{}
	// worded is set when the page carries a wording layer.
	worded bool
}

// Build verifies bundle, a held and disclosed Evidence Bundle (JSON) whose
// root is a sealed Evidence Result v0, and renders it under view.
//
// Every refusal of the bundle (including one that does not decode), its
// root or the view is an ErrRefused, and nothing is rendered. When all of those pass but the bundle is not VALID by
// `verify --bundle`'s verdict, Build returns the Page with no HTML and the
// page gate's error, so a caller can still report what it found.
func Build(bundle []byte, view View) (*Page, error) {
	if i := sort.SearchStrings(cards, view.Card); i >= len(cards) || cards[i] != view.Card {
		return nil, refusef("card must be one of %s (the contract's profile set); got %q", strings.Join(cards, ", "), view.Card)
	}
	if view.Presentation != nil {
		if err := view.Presentation.check(); err != nil {
			return nil, err
		}
	}
	var wording *emitter.Wording
	if view.Wording != nil {
		wording = &emitter.Wording{Pack: view.Wording.Pack, SHA256: view.Wording.SHA256}
		if _, err := emitter.CheckWordingPack(*wording); err != nil {
			return nil, refusal("wording: " + err.Error())
		}
	}
	value, err := cli.DecodeBundleJSON(bundle)
	if err != nil {
		return nil, refusedBy{err}
	}
	// Verify first, then gate the root, then decorate, then render: nothing
	// is rendered until everything before it has passed.
	verification, err := acceptHeldVerification(aacbundle.VerifyBundle(value))
	if err != nil {
		return nil, err
	}
	root, err := resultRootOf(value)
	if err != nil {
		return nil, err
	}
	checked, err := cli.CheckResult(root.document, "")
	if err != nil {
		return nil, refusef("root %s carries a Result that cannot be rendered: %s", root.id, cli.SafeError(err))
	}
	if root.form == FormBook {
		// The book's own verifier checks the header the record commits and
		// the checkpoint under which it sits.
		verified, err := evidencebook.VerifyBundle(bundle)
		if err != nil {
			return nil, refusal("the bundle does not verify as an evidence-book bundle: " + err.Error())
		}
		verifiedRoot := false
		for _, r := range verified.Records {
			verifiedRoot = verifiedRoot || (r.RecordID == root.id && r.HeaderVerified && r.CapsuleOK && verified.Covers(r.Seq))
		}
		if !verifiedRoot {
			return nil, refusef("root %s's disclosed header does not verify against its record, or the record is not under the bundle's checkpoint; nothing was rendered", root.id)
		}
	}
	cardBlock := map[string]interface{}{"card": view.Card}
	if view.Draft {
		cardBlock["draft"] = true
	}
	if err = setExtension(value, presentation.CardExtension, cardBlock); err != nil {
		return nil, err
	}
	if view.Presentation != nil {
		if err = setExtension(value, presentation.PresentationExtension, view.Presentation.block()); err != nil {
			return nil, err
		}
	}
	if view.ContractReport {
		if err = setExtension(value, contractReportExtension, map[string]interface{}{}); err != nil {
			return nil, err
		}
	}
	digest, err := aacbundle.BundleDigest(value)
	if err != nil {
		return nil, err
	}
	page := &Page{
		Root: root.id, Form: root.form, Member: root.member, Card: view.Card, Draft: view.Draft,
		Claims: len(checked.Claims), UnsupportedClaims: unsupportedClaims(value, checked.Claims), ContractRefs: checked.Contracts,
		BundleDigest: digest, Verification: verification, bundle: value, worded: view.Wording != nil,
	}
	// The bundle is judged as `verify --bundle` reads it from a file.
	if reread, err := json.Marshal(value); err == nil {
		if judged, err := cli.DecodeBundleJSON(reread); err == nil {
			verdict, claims := cli.BundleVerdict(judged, nil)
			page.Verdict = verdict
			// cli.UnmetClaims falls back to a failure message when no claim
			// failed, which a VALID bundle must not report.
			if verdict != "VALID" {
				page.UnmetClaims = cli.UnmetClaims(claims)
			}
		}
	}
	// A page is written only when it can stand behind what it shows: the
	// bundle must be VALID by `verify --bundle`'s own verdict, which also
	// checks every record's signature and the checkpoint's.
	if err := cli.PageGate(value); err != nil {
		return page, err
	}
	// With no wording, BuildOfflineHTML's default settings write exactly
	// EmitEvidenceGraphHTML's page.
	html, err := emitter.BuildOfflineHTML(value, pageruntime.IIFE, emitter.OfflineOptions{Audience: "*", Wording: wording, CoreRuntimeSHA256: pageruntime.SHA256})
	if err != nil {
		return nil, err
	}
	page.HTML = []byte(html)
	return page, nil
}

// Permalink is a viewer link over the same bundle the page embeds, so both
// carriers hold identical bytes; base is the viewer's bundle URL, the
// public verifier's when empty. A draft page gets none, nor does a page with
// a wording layer, which the permalink would not carry.
func (p *Page) Permalink(base string) (string, error) {
	if p.Draft {
		return "", refusal("a draft page gets no permalink")
	}
	if p.HTML == nil {
		return "", refusal("no page was rendered, so there is no permalink")
	}
	if p.worded {
		return "", refusal("a page with a wording layer gets no permalink: the permalink carries the bundle alone, and the viewer would show its own words")
	}
	fragment, err := aacbundle.EncodeFragment(p.bundle)
	if err != nil {
		return "", err
	}
	decoded, err := aacbundle.DecodeFragment(fragment)
	if err != nil {
		return "", err
	}
	if _, ok := decoded.(map[string]interface{}); !ok {
		return "", errors.New("permalink fragment did not round-trip")
	}
	if base == "" {
		base = cli.DefaultBundleURL
	}
	return strings.TrimRight(base, "#") + "#" + fragment, nil
}

// acceptHeldVerification is the acceptance rule: interval coverage and
// per-record membership must pass, and no disclosed member may mismatch;
// graph_closure may be withheld (the bundle declares records of its closure
// missing -- the claims citing them then count as unsupported, never met),
// and the label says so. Anything else refuses.
func acceptHeldVerification(result aacbundle.VerificationResult) (string, error) {
	graph := result.GraphClosure.Status
	if (graph != "pass" && graph != "withheld") || result.IntervalCoverage.Status != "pass" || result.PerRecordMembership.Status != "pass" {
		return "", refusef("the bundle does not verify (graph_closure=%s, interval_coverage=%s, per_record_membership=%s); nothing was rendered", graph, result.IntervalCoverage.Status, result.PerRecordMembership.Status)
	}
	for _, d := range result.Disclosures {
		if d.Status == disclosure.Mismatch || d.Status == disclosure.Ineligible || d.Status == disclosure.NoCommittedDigest {
			return "", refusef("the bundle does not verify: disclosed %s of %s is %s; nothing was rendered", d.Member, d.CapsuleID, d.Status)
		}
	}
	if graph == "withheld" {
		return VerificationClosureWithheld, nil
	}
	return VerificationPass, nil
}

// The book-log record types whose disclosed header stands for the capsule
// it names as its subject.
const (
	recordTypePublished  = "published_capsule"
	recordTypeBackfilled = "backfilled_entry"
)

// resultRoot is what the root gate establishes: which disclosed member of
// the root carries the Result, in which form, and the document itself.
type resultRoot struct {
	id       string
	member   string
	form     string // FormPayload | FormBook
	document map[string]interface{}
}

// resultCandidates is the viewer's dispatch test (aac ts/src/result-root.ts
// resultDocument) over one record's disclosed members, kept to every member
// that qualifies: agent_output first, then agent_input; a member IS the
// Result when it names itself one (result_version), and agent_input is the
// Result in book form when it is an evidence_result record header whose
// statement is the document (document is nil when the header carries none).
// A record that carries its header in its capsule (inline) discloses its own
// agent_input, never a header, so only the first test applies to it.
func resultCandidates(id string, members map[string]interface{}, inline bool) []resultRoot {
	var found []resultRoot
	for _, member := range []string{"agent_output", "agent_input"} {
		payload, ok := members[member].(map[string]interface{})
		if !ok {
			continue
		}
		if payload["result_version"] == cli.ResultVersion {
			found = append(found, resultRoot{id: id, member: member, form: FormPayload, document: payload})
		} else if member == "agent_input" && !inline && payload["record_type"] == evidencebook.RecordTypeEvidenceResult {
			statement, _ := payload["statement"].(map[string]interface{})
			found = append(found, resultRoot{id: id, member: member, form: FormBook, document: statement})
		}
	}
	return found
}

// resultRootOf is the Go mirror of the viewer's root gate: the root carries
// the one Result the page is built on, and no other disclosed member of the
// bundle -- the root's other member, or any other record -- carries one, so
// what the report renders is never a matter of which Result was found
// first. It names what it found when the root is not a Result.
func resultRootOf(value map[string]interface{}) (resultRoot, error) {
	id, _ := value["root"].(string)
	var rootRecord map[string]interface{}
	inline := make(map[string]bool)
	for _, raw := range asSlice(value["records"]) {
		record, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		capsuleID, _ := record["capsule_id"].(string)
		if capsuleID == id {
			rootRecord = record
		}
		if _, ok := record[evidencebook.RecordHeaderMember]; ok {
			inline[capsuleID] = true
		}
	}
	if rootRecord == nil {
		return resultRoot{}, refusef("root %s is not among the bundle's records; nothing was rendered", id)
	}
	disclosures, _ := value["disclosures"].(map[string]interface{})
	members, _ := disclosures[id].(map[string]interface{})
	// A book profile's own capsule carries its record header itself; it is
	// never an evidence_result record, which the book seals, though its
	// disclosed payload may be a Result document.
	header, ownRoot := rootRecord[evidencebook.RecordHeaderMember].(map[string]interface{})
	notResult := func() error {
		recordType, _ := header["record_type"].(string)
		return refusef("root %s is not a sealed Evidence Result v0: it is a book record of record_type %q, not %q", id, recordType, evidencebook.RecordTypeEvidenceResult)
	}
	if ownRoot && len(members) == 0 {
		return resultRoot{}, notResult()
	}
	if len(members) == 0 {
		return resultRoot{}, refusef("root %s is not a sealed Evidence Result v0: the bundle discloses nothing for it (build the bundle with disclose, not bundle)", id)
	}
	if candidates := resultCandidates(id, members, inline[id]); len(candidates) > 0 {
		if len(candidates) > 1 {
			return resultRoot{}, refusef("root %s carries a Result in both its agent_output and its agent_input; a report is rooted on exactly one Result document; nothing was rendered", id)
		}
		root := candidates[0]
		if root.document == nil {
			return resultRoot{}, refusef("root %s is an evidence_result record whose header carries no statement object; nothing was rendered", id)
		}
		others := make([]string, 0, len(disclosures))
		for other := range disclosures {
			if other != id {
				others = append(others, other)
			}
		}
		sort.Strings(others)
		for _, other := range others {
			otherMembers, _ := disclosures[other].(map[string]interface{})
			if second := resultCandidates(other, otherMembers, inline[other]); len(second) > 0 {
				return resultRoot{}, refusef("the bundle carries two Result documents: root %s and record %s (its %s); a report is rooted on exactly one Result, so build the bundle on the one it reports, with a closure that reaches no other; nothing was rendered", id, other, second[0].member)
			}
		}
		return root, nil
	}
	if ownRoot {
		return resultRoot{}, notResult()
	}
	input, _ := members["agent_input"].(map[string]interface{})
	if recordType, ok := input["record_type"].(string); ok {
		return resultRoot{}, refusef("root %s is not a sealed Evidence Result v0: its agent_input is a book record header of record_type %q, not %q", id, recordType, evidencebook.RecordTypeEvidenceResult)
	}
	for _, member := range []string{"agent_output", "agent_input"} {
		payload, _ := members[member].(map[string]interface{})
		if family, ok := payload["spec_version"].(string); ok {
			return resultRoot{}, refusef("root %s is not a sealed Evidence Result v0: its %s is a %s document", id, member, family)
		}
	}
	return resultRoot{}, refusef("root %s is not a sealed Evidence Result v0: no disclosed member carries an %s document or an %s record header", id, cli.ResultVersion, evidencebook.RecordTypeEvidenceResult)
}

func asSlice(value interface{}) []interface{} {
	items, _ := value.([]interface{})
	return items
}

// unsupportedClaims counts the claims whose cited evidence does not all
// resolve in the bundle -- by a record's capsule_id (a book profile's own
// capsule is a record, cited by that id), or by the published capsule a
// disclosed header of a wrapper that carries it names as its subject. The
// viewer renders such a claim `unsupported`, never met; this only reports
// how many.
func unsupportedClaims(value map[string]interface{}, claims []cli.ResultClaim) int {
	held := make(map[string]bool)
	// A record that carries its header inside its capsule discloses its own
	// members (agent_input is its payload), never a header.
	inline := make(map[string]bool)
	for _, raw := range asSlice(value["records"]) {
		if record, ok := raw.(map[string]interface{}); ok {
			id, _ := record["capsule_id"].(string)
			if id != "" {
				held[id] = true
			}
			// A claim cites an own capsule by its record id; the plain id
			// its header names as subject_ref is a correlation key only, as
			// the neutral viewer reads it.
			if _, ok := record[evidencebook.RecordHeaderMember]; ok {
				inline[id] = true
			}
		}
	}
	disclosures, _ := value["disclosures"].(map[string]interface{})
	for id, raw := range disclosures {
		if inline[id] {
			continue
		}
		members, _ := raw.(map[string]interface{})
		if header, ok := members["agent_input"].(map[string]interface{}); ok {
			subject, _ := header["subject_ref"].(string)
			recordType, _ := header["record_type"].(string)
			// A wrapper written before capsule-cli#236 carries its capsule as
			// payloads; an own_capsule is a record above, by its own id.
			if subject != "" && (recordType == recordTypePublished || recordType == recordTypeBackfilled) && len(asSlice(header["payload_commitments"])) > 0 {
				held[subject] = true
			}
		}
	}
	unsupported := 0
	for _, claim := range claims {
		supported := len(claim.Evidence) > 0
		for _, digest := range claim.Evidence {
			supported = supported && held[digest]
		}
		if !supported {
			unsupported++
		}
	}
	return unsupported
}

// setExtension records block under key, refusing to overwrite a different
// block already there: the embedded copy states one card and one header.
func setExtension(value map[string]interface{}, key string, block map[string]interface{}) error {
	extensions, _ := value["extensions"].(map[string]interface{})
	if extensions == nil {
		extensions = make(map[string]interface{})
	}
	if existing, present := extensions[key]; present {
		have, _ := json.Marshal(existing)
		want, _ := json.Marshal(block)
		if !bytes.Equal(have, want) {
			return refusef("the bundle already carries a different %s block (%s); build the report from a bundle without one, or pass what it states", key, have)
		}
	}
	extensions[key] = block
	value["extensions"] = extensions
	return nil
}
