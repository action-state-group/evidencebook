// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"bytes"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"unicode/utf16"
	"unicode/utf8"
)

// The entry documents of a shelf log of book facts (evidencebook#461,
// docs/design/461-shelf-facts.md §2): after the format-switch marker, every
// entry of the shelf log is the SHA-256 of one of these documents' canonical
// JSON (RFC 8785), and the document's bytes are kept beside it. A fact
// commits to two values it does not show, each as hex(SHA-256(salt ‖
// JCS(value))) under its own fresh 32-byte salt; an opening, {salt, value},
// shows one value to the audience that may have it (Openings).

// Entry document kinds.
const (
	FactKindBook          = "book_fact"
	FactKindJournal       = "journal_fact"
	EntryKindStamp        = "stamp"
	EntryKindFormatSwitch = "format_switch"
)

// ShelfFactsFormat names the shelf log format the marker switches to.
const ShelfFactsFormat = "book_facts/1"

// ProfileNone is the profile of a book no profile describes; its status is
// the empty object.
const ProfileNone = "none"

// BookFact is one book's state at a stamp (§2.2). A journal fact has the
// same members, with Size, CheckpointSHA256 and PrevFactSize zero in the
// clear and their true values inside its status commitment.
type BookFact struct {
	V                int    `json:"v"`
	Kind             string `json:"kind"`
	Profile          string `json:"profile"`
	BookID           string `json:"book_id"`
	LogID            string `json:"log_id"`
	Size             uint64 `json:"size"`
	CheckpointSHA256 string `json:"checkpoint_sha256"`
	PrevFactSize     uint64 `json:"prev_fact_size"`
	LinkCommitment   string `json:"link_commitment"`
	StatusCommitment string `json:"status_commitment"`
}

// ReceivedPosition names the last record held from the other side.
type ReceivedPosition struct {
	LogID        string `json:"log_id"`
	Size         uint64 `json:"size"`
	RecordDigest string `json:"record_digest"`
}

// LinkValue is what a fact's link commitment holds: the last record held
// from the other side, and the digests the recomputed checks ran over.
type LinkValue struct {
	LastReceived *ReceivedPosition `json:"last_received"`
	CheckedOver  []string          `json:"checked_over"`
}

// JournalStatus is a journal fact's status value: the journal's own size,
// checkpoint digest and previous fact size, which its fact does not show.
type JournalStatus struct {
	Size             uint64 `json:"size"`
	CheckpointSHA256 string `json:"checkpoint_sha256"`
	PrevFactSize     uint64 `json:"prev_fact_size"`
}

// FactOpening shows one committed value: the salt and the value.
type FactOpening struct {
	Salt  string          `json:"salt"`
	Value json.RawMessage `json:"value"`
}

// StampEntry ends every stamp, so every stamp grows the shelf log and cuts a
// new shelf checkpoint, even one with no fact (§2.5).
type StampEntry struct {
	V     int    `json:"v"`
	Kind  string `json:"kind"`
	Stamp uint64 `json:"stamp"`
	Facts uint64 `json:"facts"`
}

// LoggedSize is one log of the lifetime table at the migration.
type LoggedSize struct {
	LogID string `json:"log_id"`
	Size  uint64 `json:"size"`
}

// FormatSwitch is the one marker entry the migration appends (§4); Logs is
// every log of the lifetime table, sorted by log id.
type FormatSwitch struct {
	V        int          `json:"v"`
	Kind     string       `json:"kind"`
	Format   string       `json:"format"`
	LastTick uint64       `json:"last_tick"`
	Logs     []LoggedSize `json:"logs"`
}

// commit returns hex(SHA-256(salt ‖ JCS(value))) and the opening.
func commit(salt []byte, value any) (string, FactOpening, error) {
	if len(salt) != 32 {
		return "", FactOpening{}, fmt.Errorf("%w: a commitment's salt is 32 bytes", ErrInvalid)
	}
	jcs, err := canonicalJSON(value)
	if err != nil {
		return "", FactOpening{}, err
	}
	sum := sha256.Sum256(append(append([]byte{}, salt...), jcs...))
	return hex.EncodeToString(sum[:]), FactOpening{Salt: hex.EncodeToString(salt), Value: jcs}, nil
}

// Verify reports whether the opening recomputes commitment.
func (o FactOpening) Verify(commitment string) error {
	salt, err := hex.DecodeString(o.Salt)
	if err != nil || len(salt) != 32 || hex.EncodeToString(salt) != o.Salt {
		return fmt.Errorf("%w: an opening's salt is 32 bytes in lowercase hex", ErrInvalid)
	}
	if err := validJSONText(o.Value); err != nil {
		return fmt.Errorf("%w: an opening's value: %v", ErrInvalid, err)
	}
	value, err := decodeJSON(o.Value)
	if err != nil {
		return fmt.Errorf("%w: an opening's value: %v", ErrInvalid, err)
	}
	got, _, err := commit(salt, value)
	if err != nil {
		return err
	}
	if subtle.ConstantTimeCompare([]byte(got), []byte(commitment)) != 1 {
		return fmt.Errorf("%w: the opening does not recompute the commitment", ErrInvalid)
	}
	return nil
}

// FactInput is what a book's fact is made from at a stamp.
type FactInput struct {
	Profile          string
	BookID           string
	LogID            string
	Size             uint64
	CheckpointSHA256 string
	PrevFactSize     uint64
	Link             LinkValue
	// Status is the profile's status value as JSON; nil is {} (ProfileNone).
	Status json.RawMessage
}

// NewBookFact makes a book fact and its two openings, drawing the link salt
// then the status salt from random (§7's order).
func newBookFact(in FactInput, random randomSource) (BookFact, FactOpening, FactOpening, error) {
	if in.Profile == "" || in.BookID == "" || in.LogID == "" || in.CheckpointSHA256 == "" {
		return BookFact{}, FactOpening{}, FactOpening{}, fmt.Errorf("%w: a fact needs its profile, book, log and checkpoint", ErrInvalid)
	}
	if in.Size < in.PrevFactSize {
		return BookFact{}, FactOpening{}, FactOpening{}, fmt.Errorf("%w: a fact's size is below its previous fact's", ErrInvalid)
	}
	if !utf8.ValidString(in.Profile) || !utf8.ValidString(in.LogID) {
		return BookFact{}, FactOpening{}, FactOpening{}, fmt.Errorf("%w: a fact's profile and log id are valid UTF-8", ErrInvalid)
	}
	if !isDigest(in.BookID) || !isDigest(in.CheckpointSHA256) {
		return BookFact{}, FactOpening{}, FactOpening{}, fmt.Errorf("%w: a fact's book id and checkpoint digest are 64 lowercase hex", ErrInvalid)
	}
	status, err := statusValue(in.Status)
	if err != nil {
		return BookFact{}, FactOpening{}, FactOpening{}, err
	}
	link, err := linkValue(in.Link)
	if err != nil {
		return BookFact{}, FactOpening{}, FactOpening{}, err
	}
	linkC, linkO, statusC, statusO, err := commitBoth(link, status, random)
	if err != nil {
		return BookFact{}, FactOpening{}, FactOpening{}, err
	}
	return BookFact{V: 1, Kind: FactKindBook, Profile: in.Profile, BookID: in.BookID, LogID: in.LogID,
		Size: in.Size, CheckpointSHA256: in.CheckpointSHA256, PrevFactSize: in.PrevFactSize,
		LinkCommitment: linkC, StatusCommitment: statusC}, linkO, statusO, nil
}

// newJournalFact makes the journal's fact: its book id is the SHA-256 of the
// journal's log id, and its size, checkpoint digest and previous size are
// inside its status commitment only.
func newJournalFact(logID string, status JournalStatus, random randomSource) (BookFact, FactOpening, FactOpening, error) {
	if logID == "" || !utf8.ValidString(logID) || !isDigest(status.CheckpointSHA256) || status.Size < status.PrevFactSize {
		return BookFact{}, FactOpening{}, FactOpening{}, fmt.Errorf("%w: a journal fact needs its log, checkpoint and sizes", ErrInvalid)
	}
	sum := sha256.Sum256([]byte(logID))
	link, err := linkValue(LinkValue{})
	if err != nil {
		return BookFact{}, FactOpening{}, FactOpening{}, err
	}
	linkC, linkO, statusC, statusO, err := commitBoth(link, status, random)
	if err != nil {
		return BookFact{}, FactOpening{}, FactOpening{}, err
	}
	return BookFact{V: 1, Kind: FactKindJournal, Profile: ProfileNone, BookID: hex.EncodeToString(sum[:]), LogID: logID,
		LinkCommitment: linkC, StatusCommitment: statusC}, linkO, statusO, nil
}

func commitBoth(link, status any, random randomSource) (string, FactOpening, string, FactOpening, error) {
	linkSalt, err := random.bytes(32)
	if err != nil {
		return "", FactOpening{}, "", FactOpening{}, err
	}
	statusSalt, err := random.bytes(32)
	if err != nil {
		return "", FactOpening{}, "", FactOpening{}, err
	}
	linkC, linkO, err := commit(linkSalt, link)
	if err != nil {
		return "", FactOpening{}, "", FactOpening{}, err
	}
	statusC, statusO, err := commit(statusSalt, status)
	if err != nil {
		return "", FactOpening{}, "", FactOpening{}, err
	}
	return linkC, linkO, statusC, statusO, nil
}

// statusValue is the profile's status: absent is {}, and anything else must
// be a JSON object (null included is refused). Its numbers are integers, as
// every value the layer canonicalizes (AAC §5.1 refuses floats in
// digest-bearing values, in Go, Rust and Python alike).
func statusValue(raw json.RawMessage) (any, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return map[string]any{}, nil
	}
	if err := validJSONText(raw); err != nil {
		return nil, fmt.Errorf("%w: a fact's status: %v", ErrInvalid, err)
	}
	v, err := decodeJSON(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: a fact's status: %v", ErrInvalid, err)
	}
	if _, ok := v.(map[string]any); !ok {
		return nil, fmt.Errorf("%w: a fact's status is a JSON object", ErrInvalid)
	}
	return v, nil
}

// linkValue checks the link's members and sorts the checked-over digests,
// as the design fixes them.
func linkValue(l LinkValue) (LinkValue, error) {
	// A record held from the other side means its log has at least one entry,
	// so a size of 0 (also what a missing size reads as in Go) is refused.
	if r := l.LastReceived; r != nil && (r.LogID == "" || !utf8.ValidString(r.LogID) || r.Size == 0 || !isDigest(r.RecordDigest)) {
		return LinkValue{}, fmt.Errorf("%w: last_received needs its log id, a size of at least 1 and a SHA-256 record digest", ErrInvalid)
	}
	over := append([]string{}, l.CheckedOver...)
	for _, d := range over {
		if !isDigest(d) {
			return LinkValue{}, fmt.Errorf("%w: a checked-over digest is 64 lowercase hex", ErrInvalid)
		}
	}
	sort.Strings(over)
	for i := 1; i < len(over); i++ {
		if over[i] == over[i-1] {
			return LinkValue{}, fmt.Errorf("%w: a digest checked over twice", ErrInvalid)
		}
	}
	return LinkValue{LastReceived: l.LastReceived, CheckedOver: over}, nil
}

// EntryID is a shelf log entry: the SHA-256 of an entry document's
// canonical JSON, and those bytes.
func EntryID(doc any) ([32]byte, []byte, error) {
	jcs, err := canonicalJSON(doc)
	if err != nil {
		return [32]byte{}, nil, err
	}
	return sha256.Sum256(jcs), jcs, nil
}

// newFormatSwitch makes the marker, its logs sorted by log id; each log is
// listed once.
func newFormatSwitch(lastTick uint64, logs []LoggedSize) (FormatSwitch, error) {
	sorted := append([]LoggedSize{}, logs...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].LogID < sorted[j].LogID })
	for i := range sorted {
		if sorted[i].LogID == "" || !utf8.ValidString(sorted[i].LogID) || (i > 0 && sorted[i].LogID == sorted[i-1].LogID) {
			return FormatSwitch{}, fmt.Errorf("%w: the marker lists each log once, by its id", ErrInvalid)
		}
	}
	return FormatSwitch{V: 1, Kind: EntryKindFormatSwitch, Format: ShelfFactsFormat, LastTick: lastTick, Logs: sorted}, nil
}

// VerifyOpenings checks the openings a reader was handed for a fact: each one
// given (nil is not given) must recompute its commitment. Which openings an
// audience may be handed is OpeningsFor's; the shelf's per-book Openings call
// lands with the stamp (docs/design/461-shelf-facts.md §6, part 2).
func VerifyOpenings(fact BookFact, link, status *FactOpening) error {
	if link != nil {
		if err := link.Verify(fact.LinkCommitment); err != nil {
			return fmt.Errorf("link opening: %w", err)
		}
	}
	if status != nil {
		if err := status.Verify(fact.StatusCommitment); err != nil {
			return fmt.Errorf("status opening: %w", err)
		}
	}
	return nil
}

// Audience is who an opening is handed to (§2.2).
type Audience string

// The audiences of a fact's openings.
const (
	AudienceCounterparty Audience = "counterparty"
	AudienceAdjudicator  Audience = "adjudicator"
	// AudienceAsker is an asker the holder's disclosure pack lets see the
	// deal.
	AudienceAsker Audience = "asker"
	// AudienceChosenReader is a reader the holder chooses, such as a card
	// reader handed the whole shelf.
	AudienceChosenReader Audience = "chosen_reader"
	AudienceOwner        Audience = "owner"
	AudienceAuditor      Audience = "auditor"
)

// OpeningsFor says which of a fact's openings an audience may have: the
// link opening of a book fact goes to the counterparty, the adjudicator and
// an asker the disclosure pack lets in; its status opening to the
// counterparty and a reader the holder chooses; a journal fact's openings to
// the owner and an auditor it appoints, and to nobody else. The owner holds
// every opening of its own shelf.
func OpeningsFor(kind string, audience Audience) (link, status bool, err error) {
	switch kind {
	case FactKindBook:
		switch audience {
		case AudienceCounterparty:
			return true, true, nil
		case AudienceAdjudicator, AudienceAsker:
			return true, false, nil
		case AudienceChosenReader:
			return false, true, nil
		case AudienceOwner:
			return true, true, nil
		case AudienceAuditor:
			return false, false, nil
		}
	case FactKindJournal:
		switch audience {
		case AudienceOwner, AudienceAuditor:
			return true, true, nil
		case AudienceCounterparty, AudienceAdjudicator, AudienceAsker, AudienceChosenReader:
			return false, false, nil
		}
	default:
		return false, false, fmt.Errorf("%w: no openings for %q", ErrInvalid, kind)
	}
	return false, false, fmt.Errorf("%w: unknown audience %q", ErrInvalid, audience)
}

func isDigest(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// validJSONText refuses invalid UTF-8 and an escaped surrogate that is not
// part of a pair, which Go's decoder would silently replace with U+FFFD where
// Rust refuses it and Python keeps it.
func validJSONText(b []byte) error {
	if !utf8.Valid(b) {
		return errors.New("invalid UTF-8")
	}
	inString := false
	for i := 0; i < len(b); i++ {
		switch {
		case !inString:
			inString = b[i] == '"'
		case b[i] == '"':
			inString = false
		case b[i] == '\\' && i+1 < len(b):
			i++
			if b[i] != 'u' || i+4 >= len(b) {
				continue
			}
			r := hex4(b[i+1 : i+5])
			i += 4
			if !utf16.IsSurrogate(r) {
				continue
			}
			if r >= 0xdc00 || i+6 >= len(b) || b[i+1] != '\\' || b[i+2] != 'u' {
				return errors.New("an unpaired surrogate")
			}
			if lo := hex4(b[i+3 : i+7]); lo < 0xdc00 || lo > 0xdfff {
				return errors.New("an unpaired surrogate")
			}
			i += 6
		}
	}
	return nil
}

func hex4(b []byte) rune {
	var r rune
	for _, c := range b {
		r <<= 4
		switch {
		case c >= '0' && c <= '9':
			r |= rune(c - '0')
		case c >= 'a' && c <= 'f':
			r |= rune(c-'a') + 10
		case c >= 'A' && c <= 'F':
			r |= rune(c-'A') + 10
		}
	}
	return r
}
