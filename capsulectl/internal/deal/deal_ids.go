package deal

import (
	"errors"
	"regexp"
	"strings"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook/dealprofile"
)

// Counterparty identifiers never enter a sealed record raw. A record carries
// a per-deal HMAC fingerprint of each normalized identifier, and free text
// that may contain identifiers is carried as a salted commitment. Raw values,
// the per-deal key and the commitment nonces stay in the local store. The
// construction, its keys and the record scans are EvidenceBook's dealprofile
// (PROFILE.md §4).

// sameID compares two raw identifiers the way their fingerprints would:
// normalized. When either side cannot be normalized, it falls back to a
// case-insensitive comparison of the trimmed text.
func sameID(kind, a, b string) bool {
	na, ea := dealprofile.Normalize(kind, a)
	nb, eb := dealprofile.Normalize(kind, b)
	if ea == nil && eb == nil {
		return na == nb
	}
	return strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b))
}

// scanRecord runs the profile's record-level scans (personal_data and
// wording) over record, which need not be a whole record yet: a record that
// would carry a raw phone number, email address or local-store value, or a
// word that labels or grades someone, is refused. Its schema is checked when
// the whole record is sealed (encodeDealRecord).
func scanRecord(record map[string]interface{}, localValues []string) error {
	doc := map[string]interface{}{}
	for k, v := range record {
		doc[k] = v
	}
	if _, ok := doc[dealprofile.Profile]; !ok {
		doc[dealprofile.Profile] = map[string]interface{}{"canonicalization": dealprofile.Canonicalization}
	}
	err := dealprofile.Check(doc, localValues)
	var se *dealprofile.StageError
	if errors.As(err, &se) && se.Stage == dealprofile.StageSchema {
		return nil
	}
	return sealError(err)
}

var prohibitedWord = regexp.MustCompile(`^prohibited word "([^"]*)"`)

// sealError is a record's refusal by the profile's checks (a
// *dealprofile.StageError) at a stage before the schema, in the words a user
// reads: a hit is a bug in the producer, so the step is not sealed.
// checkSealable words a schema refusal.
func sealError(err error) error {
	var se *dealprofile.StageError
	if !errors.As(err, &se) {
		return err
	}
	switch se.Stage {
	case dealprofile.StagePersonalData:
		switch {
		case strings.HasPrefix(se.Detail, "raw phone number"):
			return cli.InputError("refusing to seal: the record would carry a raw phone number; keep it out of claims and tokens")
		case strings.HasPrefix(se.Detail, "raw email address"):
			return cli.InputError("refusing to seal: the record would carry a raw email address")
		default:
			return cli.InputError("refusing to seal: the record would carry a raw counterparty identifier")
		}
	case dealprofile.StageWording:
		word := se.Detail
		if m := prohibitedWord.FindStringSubmatch(se.Detail); m != nil {
			word = m[1]
		}
		return cli.InputError("refusing to seal: a record may not label or grade anyone (" + word + ")")
	default:
		return cli.InputError("refusing to seal: " + se.Detail)
	}
}

// dealSealsCounterpartyProfile is whether a check is followed by its
// counterparty_profile step; a test turns it off to stand in for a release
// from before the step existed.
var dealSealsCounterpartyProfile = true

// counterpartyProfileStep is the counterparty_profile step for a check, or
// nil when the check names no payee (or one that does not normalize).
func (s *dealSession) counterpartyProfileStep(snap dealSnapshot) (*dealCounterpartyProfile, error) {
	if !dealSealsCounterpartyProfile || snap.Who == nil || snap.Who.Payee == "" {
		return nil, nil
	}
	fp, err := dealprofile.Fingerprint(dealprofile.ProfileKey(s.secret), "payee", snap.Who.Payee)
	if errors.Is(err, dealprofile.ErrNotNormalizable) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &dealCounterpartyProfile{Payee: fp}, nil
}
