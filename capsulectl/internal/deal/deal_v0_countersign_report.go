// SPDX-License-Identifier: Apache-2.0

package deal

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"time"

	"github.com/action-state-group/agent-action-capsule/go/canonical"
	"github.com/action-state-group/capsule-cli/cli"
)

// An x-deal-v0 copy's countersignatures, as `deal verify --bundle` reports
// them for a page (capsule-viewer#383, CV3b; the contract is capsule-viewer's
// tests/testdata/deal-v0-report/README.md). With --countersign-directory D,
// the copy's one x-deal-v0 entry carries
//
//	countersign: {directory, countersignatures_sha256, entries}
//
// entries being cli.VerifyCountersignatures's reports as it returns them, in
// the bundle's order, and countersignatures_sha256 the lowercase hex SHA-256
// of the JCS of the bundle's whole countersignatures array, which binds the
// member to the countersignatures it was computed over (they sit outside the
// bundle digest). A page draws line 9 from a bound member in capsulectl's
// voice and refuses a copy whose bound member holds an invalid entry, as
// `deal report` refuses to show one. The member never changes the verdict.
//
// verify loads no profile, so the producer's own keys come as
// --producer-key; `deal report --html` passes the profile's trusted_keys, as
// its own check used them (dealCountersignVerify).

// countersignDirectoryFlag and producerKeyFlag are verify's flags for it.
const (
	countersignDirectoryFlag = "countersign-directory"
	producerKeyFlag          = "producer-key"
)

// countersignVerifyTimeout bounds one directory fetch, as deal report's own
// check does.
const countersignVerifyTimeout = 10 * time.Second

// v0CountersignMember is the countersign member for the copy value: nil when
// it carries no countersignatures. When they cannot be checked (the
// directory cannot be read, or the array is malformed), the member is
// {directory, error}: verify's report is printed whatever the member says,
// so the failure rides in it rather than after it, a page reads such a
// member as malformed and ignores it, and `deal report --html` refuses it.
func v0CountersignMember(ctx context.Context, value map[string]interface{}, directory string, producerKeys []ed25519.PublicKey) map[string]any {
	carried, present := value["countersignatures"]
	if !present {
		return nil
	}
	if list, ok := carried.([]interface{}); ok && len(list) == 0 {
		return nil
	}
	failed := func(why string) map[string]any { return map[string]any{"directory": directory, "error": why} }
	canon, err := canonical.JCS(carried)
	if err != nil {
		return failed("the bundle's countersignatures cannot be canonicalized: " + err.Error())
	}
	sum := sha256.Sum256(canon)
	client := &http.Client{Timeout: countersignVerifyTimeout}
	_, reports, _, err := cli.VerifyCountersignatures(ctx, client, directory, value, producerKeys)
	if err != nil {
		return failed(err.Error())
	}
	entries := make([]any, len(reports))
	for i, r := range reports {
		entries[i] = r
	}
	return map[string]any{"directory": directory, "countersignatures_sha256": hex.EncodeToString(sum[:]), "entries": entries}
}
