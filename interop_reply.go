// SPDX-License-Identifier: Apache-2.0

package evidencebook

import "github.com/action-state-group/agent-action-capsule/go/evidencerequest"

// Verification-material kinds (profile §6.2.1), the Checkpointed Local Log
// mechanism's proof formats.
const (
	KindCLLCheckpoint       = evidencerequest.KindCLLCheckpoint
	KindCLLInclusionProof   = evidencerequest.KindCLLInclusionProof
	KindCLLConsistencyProof = evidencerequest.KindCLLConsistencyProof
)

// The reply types are the request draft's, defined in go/evidencerequest
// (ER §4.1, §4.2, profile §6.2, §6.3); these names keep evidencebook's API.
type (
	Refusal           = evidencerequest.Refusal
	Anchor            = evidencerequest.Anchor
	VerificationEntry = evidencerequest.VerificationEntry
	Envelope          = evidencerequest.Envelope
	Reply             = evidencerequest.Reply
)

// ParseReply reads a received reply: exactly one of an envelope or a
// refusal. Every failure matches ErrInvalid.
func ParseReply(m Message) (Reply, error) {
	return evidencerequest.ParseReply(m)
}
