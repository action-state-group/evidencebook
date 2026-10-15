// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"fmt"
	"github.com/action-state-group/agent-action-capsule/go/evidencerequest"
	"slices"
)

// Binding tokens a manifest may list (profile §4, §11.4.2).
const (
	BindingStream = "evidence-request/1"
	BindingHTTP   = "http"
	BindingA2A    = "a2a"
	BindingMCP    = "mcp"
)

// ManifestOptions is what the host states about how it carries the
// exchange. The book knows its own profile, drafts, subject forms,
// derivations and refusal policy; it does not know its carriers.
type ManifestOptions struct {
	// Bindings are the binding tokens the host answers this exchange over.
	Bindings []string
	// Retention is a URI where the responder's retention commitments can be
	// fetched, or empty. It must not be an enumerable index open to
	// unauthenticated callers (profile §11.4.3).
	Retention string
}

// Manifest returns the responder's manifest (ER §10, profile §11.4.2): a
// JSON object, in JCS, whose every value is a text string or a sorted array
// of text strings. It says what can be asked, never what exists: it carries
// no count, digest, anchor, time, principal, counterparty or history card
// (profile §11.4.3), and it is the same for every caller. The book's
// discovery index (Discover) is never part of it.
func (b *Book) Manifest(opts ManifestOptions) ([]byte, error) {
	bindings := slices.Clone(opts.Bindings)
	slices.Sort(bindings)
	bindings = slices.Compact(bindings)
	if len(bindings) == 0 {
		return nil, fmt.Errorf("%w: a manifest lists at least one binding", ErrInvalid)
	}
	for _, binding := range bindings {
		if !slices.Contains([]string{BindingStream, BindingHTTP, BindingA2A, BindingMCP}, binding) {
			return nil, fmt.Errorf("%w: %q is not a binding token", ErrInvalid, binding)
		}
	}
	forms := make([]any, len(SubjectForms))
	for i, form := range SubjectForms {
		forms[i] = string(form)
	}
	listed := make([]any, len(bindings))
	for i, binding := range bindings {
		listed[i] = binding
	}
	policy := b.responder.RefusalPolicy
	if policy == "" {
		policy = RefusalPolicySpecific
	}
	manifest := map[string]any{
		"profiles":       []any{ProfileID},
		"drafts":         []any{RequestDraft},
		"subject_forms":  forms,
		"derivations":    []any{DerivationHistoryCard},
		"bindings":       listed,
		"refusal_policy": policy,
	}
	if opts.Retention != "" {
		manifest["retention"] = opts.Retention
	}
	return evidencerequest.EncodeJCS(manifest)
}
