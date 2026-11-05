package deal

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"slices"
	"strings"

	"github.com/action-state-group/agent-action-capsule/go/canonical"
	"github.com/action-state-group/agent-action-capsule/go/standardhistory"
	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook/dealprofile"
	"github.com/spf13/cobra"
)

// The dispute and the ruling (evidencebook-deal#151, Terms Pack Studio step 6). Both stand in
// for the dispute and ruling record kinds until those land (evidencebook#404): each is an
// ordinary evidence step, so no record family and no schema is added. What tells them apart is
// the evidence's source token, which sits in clear in the sealed record:
//
//	dispute            the buyer disputes one row of the seller's terms
//	ruling_for_buyer   the referee's one ruling, for the party who disputed
//	ruling_for_seller  the referee's one ruling, against the party who disputed
//
// The words and the rest of the body are the step's detail, sealed as the usual commitment. The
// ruling digest is the SHA-256 of the ruling body; the referee writes the same body to both
// parties' stores, so both compute the same digest (record digests differ per store).
const (
	noteKindDispute   = "dispute"
	noteKindRuling    = "ruling"
	sourceDispute     = "dispute"
	sourceRulingBuyer = "ruling_for_buyer"
	sourceRulingSell  = "ruling_for_seller"

	findingForBuyer  = "for_buyer"
	findingForSeller = "for_seller"
)

// rulingShown is how many leading hex characters of the ruling digest a receipt line shows.
const rulingShown = 12

// disputeBody is a dispute's detail: the row and the buyer's own words.
type disputeBody struct {
	Row   string `json:"row"`
	Words string `json:"words"`
}

// rulingBody is a ruling's detail. Copies are the SHA-256 of the two parties' adjudicator copies
// the referee checked, in sorted order, so the body is the same whichever store it is written to.
type rulingBody struct {
	Referee string   `json:"referee"`
	Row     string   `json:"row"`
	Finding string   `json:"finding"`
	Copies  []string `json:"copies"`
	Dispute string   `json:"dispute"`
}

func isDisputeSource(source string) bool {
	return source == sourceDispute || source == sourceRulingBuyer || source == sourceRulingSell
}

// bodyDigest is the SHA-256 of a string, hex. For a ruling it is taken over the step's detail, the body's RFC 8785 bytes.
func bodyDigest(detail string) string {
	sum := sha256.Sum256([]byte(detail))
	return hex.EncodeToString(sum[:])
}

func disputeJCS(v any) (string, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return "", err
	}
	b, err := canonical.JCS(doc)
	return string(b), err
}

// disputeNoteEvent builds the evidence step for `deal note --kind dispute|ruling` from its flags.
func disputeNoteEvent(c *cobra.Command, kind string) (dealEvent, error) {
	flag := func(name string) string { v, _ := c.Flags().GetString(name); return strings.TrimSpace(v) }
	if flag("input") != "" {
		return dealEvent{}, cli.InputError(kind + " is built from its flags, not --input")
	}
	row := asToken(flag("row"), "")
	if row == "" {
		return dealEvent{}, cli.InputError(kind + " needs --row: the pack row it is about, as the pack names it (price_as_offered)")
	}
	var body any
	source := sourceDispute
	switch kind {
	case noteKindDispute:
		words := flag("words")
		if words == "" {
			return dealEvent{}, cli.InputError("dispute needs --words: what the buyer says was wrong")
		}
		body = disputeBody{Row: row, Words: words}
	default:
		finding, referee, dispute := flag("finding"), flag("referee"), flag("dispute-digest")
		copies, _ := c.Flags().GetStringArray("copy")
		switch {
		case finding != findingForBuyer && finding != findingForSeller:
			return dealEvent{}, cli.InputError("ruling needs --finding for_buyer or for_seller")
		case referee == "":
			return dealEvent{}, cli.InputError("ruling needs --referee: who rules")
		case len(dispute) != 64 || !cli.IsLowerHex(dispute):
			return dealEvent{}, cli.InputError("ruling needs --dispute-digest: the SHA-256 the dispute note printed (64 lowercase hex)")
		case len(copies) != 2:
			return dealEvent{}, cli.InputError("ruling needs two --copy files: each party's `disclose --share adjudicator` file, the ones `terms-result --check` ran over")
		}
		digests := make([]string, len(copies))
		for i, path := range copies {
			raw, err := os.ReadFile(path)
			if err != nil {
				return dealEvent{}, cli.InputError("cannot read --copy " + path)
			}
			sum := sha256.Sum256(raw)
			digests[i] = hex.EncodeToString(sum[:])
		}
		slices.Sort(digests)
		if digests[0] == digests[1] {
			return dealEvent{}, cli.InputError("the two --copy files are the same: a ruling cites both parties' copies")
		}
		body = rulingBody{Referee: referee, Row: row, Finding: finding, Copies: digests, Dispute: dispute}
		source = sourceRulingBuyer
		if finding == findingForSeller {
			source = sourceRulingSell
		}
	}
	detail, err := disputeJCS(body)
	if err != nil {
		return dealEvent{}, err
	}
	// The second store's ruling names the first one's digest, so a flag mistyped there is refused instead of
	// sealed for good with a digest that differs from the other party's.
	if want := flag("ruling-digest"); want != "" && kind == noteKindRuling && want != bodyDigest(detail) {
		return dealEvent{}, cli.InputError("this ruling's digest is not " + want + ": it differs from the other store's ruling in a flag (--row, --finding, --referee, --dispute-digest or a --copy file)")
	}
	return dealEvent{Kind: "evidence", Evidence: &dealEvidence{About: row, Source: source, Detail: detail}}, nil
}

// checkDisputeNote holds a dispute or a ruling to the deal it is sealed on: the buyer disputes, once and
// only before the ruling; one ruling per deal, whichever party's store holds it. Where the store holds
// the dispute (the buyer's), the ruling must cite that dispute (the reference derived from its record digest, disputeRef) and its row; the
// seller's store holds no dispute, so it takes the ruling's citation as given.
func checkDisputeNote(events []sealedEvent, ev dealEvent) error {
	source := ev.Evidence.Source
	var dispute *sealedEvent
	ruled := false
	for i, se := range events {
		if se.Event.Evidence == nil {
			continue
		}
		switch se.Event.Evidence.Source {
		case sourceDispute:
			dispute = &events[i]
		case sourceRulingBuyer, sourceRulingSell:
			ruled = true
		}
	}
	switch {
	case source == sourceDispute && dealRole(events) != dealRoleBuyer:
		return cli.InputError("the buyer disputes: this deal's user is the seller, whose store takes the ruling")
	case ruled:
		return cli.InputError("this deal already holds a ruling: a ruling is final, and there is no second one")
	case source == sourceDispute && dispute != nil:
		return cli.InputError("this deal already holds a dispute: one dispute per deal")
	case source == sourceDispute:
		return nil
	case dealRole(events) == dealRoleBuyer && dispute == nil:
		return cli.InputError("this deal holds no dispute to rule on: the buyer disputes first (deal note --kind dispute)")
	case dispute != nil:
		var b rulingBody
		if err := json.Unmarshal([]byte(ev.Evidence.Detail), &b); err != nil {
			return err
		}
		if b.Dispute != disputeRef(dispute.Digest) || b.Row != dispute.Event.Evidence.About {
			return cli.InputError("the ruling must cite this deal's dispute: its --dispute-digest and --row are the ones the dispute note printed")
		}
	}
	return nil
}

// rulingOf is a deal's one ruling as its store holds it: its row, who it found for, and the digest of
// its body.
type rulingOf struct {
	Row      string
	ForBuyer bool
	Digest   string
}

// rulingOfEvent reads a ruling step. ok is false for any other step, and for a ruling whose body
// does not read, so a ruling the store cannot read is not counted.
func rulingOfEvent(e dealEvent) (rulingOf, bool) {
	ev := e.Evidence
	if ev == nil || (ev.Source != sourceRulingBuyer && ev.Source != sourceRulingSell) {
		return rulingOf{}, false
	}
	var b rulingBody
	if json.Unmarshal([]byte(ev.Detail), &b) != nil {
		return rulingOf{}, false
	}
	return rulingOf{Row: ev.About, ForBuyer: ev.Source == sourceRulingBuyer, Digest: bodyDigest(ev.Detail)}, true
}

// rulingIn finds the ruling a deal's events hold.
func rulingIn(events []sealedEvent) (rulingOf, bool) {
	for _, se := range events {
		if r, ok := rulingOfEvent(se.Event); ok {
			return r, true
		}
	}
	return rulingOf{}, false
}

// disputeRef is the reference a ruling cites for a dispute: the SHA-256 of the dispute step's record digest.
// The record digest commits to the dispute's salted detail, so the reference cannot be tested against a
// guess at the buyer's words; and it is not the record digest itself, which a shared copy lists, so the
// ruling's sealed detail never holds a value the page carries.
func disputeRef(recordDigest string) string { return bodyDigest("dispute-ref:" + recordDigest) }

// rowWords reads a row token as words ("price_as_offered" is "price as offered").
func rowWords(row string) string { return strings.ReplaceAll(row, "_", " ") }

func winnerWords(forBuyer bool) string {
	if forBuyer {
		return "the buyer"
	}
	return "the seller"
}

// rulingLine is the receipt's one line for the ruling, the same words on both parties' copies:
// `Ruling <12 hex> on "price as offered": found for the buyer`. It is returned in two parts, the
// words and the 12 hex, because a shared copy scrubs words and would take the hex for a code.
func rulingLine(r rulingOf) (line, digest string) {
	return "on \"" + rowWords(r.Row) + "\": found for " + winnerWords(r.ForBuyer), r.Digest[:rulingShown]
}

// disputesOf is a deal's SH §5.4 disputes as its records under the anchor state them (evidencebook-deal#151).
// The profile seals no disputes statement of its own, so the dispute and ruling steps stand in: the source
// token of an evidence record carries the finding in clear. role is the holder's side of the entry
// (standardhistory's "bought" or "provided"). A ruling ends the dispute at once, for or against the holder;
// a dispute with no ruling yet is open, and only a buyer's store holds one. No ruling and no dispute is none.
func disputesOf(records []dealprofile.HistoryRecord, role string) []standardhistory.Dispute {
	disputed, ruled := false, ""
	for _, r := range records {
		var rec struct {
			X struct {
				RecordType string `json:"record_type"`
			} `json:"x-deal-v0"`
			Body struct {
				Source string `json:"source"`
			} `json:"body"`
		}
		if json.Unmarshal(r.Payload, &rec) != nil || rec.X.RecordType != "evidence" {
			continue
		}
		switch rec.Body.Source {
		case sourceDispute:
			disputed = true
		case sourceRulingBuyer, sourceRulingSell:
			ruled = rec.Body.Source
		}
	}
	d := standardhistory.Dispute{Side: "received", Outcome: "open", Forum: "external"}
	if role == "bought" {
		d.Side = "raised"
	}
	switch {
	case ruled != "":
		d.Outcome = "against"
		if (ruled == sourceRulingBuyer) == (role == "bought") {
			d.Outcome = "for"
		}
	case disputed && role == "bought":
	default:
		return []standardhistory.Dispute{}
	}
	return []standardhistory.Dispute{d}
}
