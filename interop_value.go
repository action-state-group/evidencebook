// SPDX-License-Identifier: Apache-2.0

package evidencebook

import "github.com/action-state-group/agent-action-capsule/go/evidencerequest"

// The two encodings a request and its reply travel in (profile §4, §11.4.1
// rule 7). The wire values, their strict decoders and deterministic encoders
// live in go/evidencerequest; these names keep evidencebook's API.
const (
	MediaTypeJSON = evidencerequest.MediaTypeJSON
	MediaTypeCBOR = evidencerequest.MediaTypeCBOR
)

// Message is octets as carried, with the media type they were carried under.
type Message = evidencerequest.Message
