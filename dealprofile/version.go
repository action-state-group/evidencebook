// SPDX-License-Identifier: Apache-2.0

package dealprofile

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
)

// ErrUnknownVersion reports a deal record whose shape names no profile
// version: a reader refuses it as unknown, never as invalid, and never reads
// it as another version.
var ErrUnknownVersion = errors.New("dealprofile: not a deal record of a known shape")

var versionName = regexp.MustCompile(`^x-deal-v[0-9]+$`)

// typedVersionTypes are the typed records and contract types a typed v0 record's
// type names (PROFILE.md §9).
var typedVersionTypes = map[string]bool{
	TypeTaskAuthority: true, TypeProposedAction: true, TypeActionEvaluation: true, TypeActionApproval: true,
	TypeActionRecord: true, TypeActionOutcome: true, TypeActionReport: true, TypeCheckRequest: true,
	TypeCheckResponse: true, TypeCommercialBounds: true,
}

// Version is the profile version a deal record is of (evidencebook#461,
// docs/design/461-deal-profile.md §2.3): the name of its one top-level
// member matching x-deal-v<n>, whose block's profile equals it; or
// x-deal-v0 for a typed v0 record, which carries no block and whose type is
// one of PROFILE.md §9's typed record and contract types. A record with two
// such members, with a block and a typed type, with neither, or whose
// block's profile is not its name is ErrUnknownVersion. Version reads only
// the shape; a reader of that version checks the rest.
func Version(record []byte) (string, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(record, &top); err != nil {
		return "", fmt.Errorf("%w: %v", ErrUnknownVersion, err)
	}
	var name string
	for k := range top {
		if !versionName.MatchString(k) {
			continue
		}
		if name != "" {
			return "", fmt.Errorf("%w: two profile blocks", ErrUnknownVersion)
		}
		name = k
	}
	var typed string
	if raw, ok := top["type"]; ok {
		_ = json.Unmarshal(raw, &typed)
	}
	switch {
	case name != "" && typedVersionTypes[typed]:
		return "", fmt.Errorf("%w: a profile block and a typed type", ErrUnknownVersion)
	case name != "":
		var blk struct {
			Profile string `json:"profile"`
		}
		if err := json.Unmarshal(top[name], &blk); err != nil || blk.Profile != name {
			return "", fmt.Errorf("%w: the %s block's profile is not %s", ErrUnknownVersion, name, name)
		}
		return name, nil
	case typedVersionTypes[typed]:
		return Profile, nil
	}
	return "", fmt.Errorf("%w: no profile block and no typed type", ErrUnknownVersion)
}
