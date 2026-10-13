// SPDX-License-Identifier: Apache-2.0

// Command responder answers the responder-side requests of the evidence
// interoperability profile's conformance vectors with a fresh book, and
// writes every request and reply as carried, for checking by an independent
// verifier (test/interop/python-profile/verify_responder.py runs the AAC
// reference verifier over them).
//
//	go run ./test/interop/responder <dir> <out.json>
//
// The book signs with the vectors' test keys (each an Ed25519 seed that is
// the SHA-256 of a public label), so the vectors' trust pins name it. The
// requests are the vectors' requests made against this book's own log: the
// same subject form, coverage form, nonces and profile member, with this
// book's record digests and anchors in place of the vectors'.
package main

import (
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/action-state-group/evidencebook"
)

const (
	responderLabel = "draft-mih-agent-evidence-request-00 conformance vectors: primary responder key"
	logLabel       = "evidence-interop/0 worked example: CLL log key"
	logID          = "evidence-stream-1"
)

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: responder <dir> <out.json>")
		os.Exit(2)
	}
	if err := run(context.Background(), os.Args[1], os.Args[2]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func seeded(label string) ed25519.PrivateKey {
	seed := sha256.Sum256([]byte(label))
	return ed25519.NewKeyFromSeed(seed[:])
}

type limitNone struct{}

func (limitNone) Allow(string, time.Time) bool { return false }

func open(ctx context.Context, dir string, policy evidencebook.ResponderPolicy, clock *time.Time) (*evidencebook.Book, error) {
	store, err := evidencebook.OpenFileStore(filepath.Join(dir, "records"))
	if err != nil {
		return nil, err
	}
	substrate, err := evidencebook.OpenCLL(filepath.Join(dir, "cll.jsonl"), logID, seeded(logLabel))
	if err != nil {
		return nil, err
	}
	payloads, err := evidencebook.OpenPayloadDir(filepath.Join(dir, "payloads"))
	if err != nil {
		return nil, err
	}
	signer, err := evidencebook.NewEd25519Signer(seeded(responderLabel))
	if err != nil {
		return nil, err
	}
	return evidencebook.Open(ctx, evidencebook.Config{
		BookID: "responder", Operator: "responder-operator", Store: store, Substrate: substrate, Payloads: payloads, Signer: signer,
		Now:       func() time.Time { *clock = clock.Add(time.Second); return *clock },
		Responder: policy,
	})
}

// carried is octets as the vectors write them: JSON as text, CBOR as hex.
type carried struct {
	MediaType  string `json:"media_type"`
	OctetsText string `json:"octets_text,omitempty"`
	OctetsHex  string `json:"octets_hex,omitempty"`
}

func carry(m evidencebook.Message) carried {
	if m.MediaType == evidencebook.MediaTypeJSON {
		return carried{MediaType: m.MediaType, OctetsText: string(m.Octets)}
	}
	return carried{MediaType: m.MediaType, OctetsHex: hex.EncodeToString(m.Octets)}
}

type exchange struct {
	Requests      []carried `json:"requests"`
	Replies       []carried `json:"replies"`
	AbsenceRecord *carried  `json:"absence_record,omitempty"`
}

type output struct {
	ResponderKey string              `json:"responder_key"`
	LogKeys      []map[string]string `json:"log_keys"`
	Exchanges    map[string]exchange `json:"exchanges"`
	Invariance   map[string]string   `json:"invariance_artifacts_hex"`
	RecordSet    map[string]string   `json:"invariance_record_set_artifacts_hex"`
	Requests     map[string]carried  `json:"other_requests"`
	Manifests    []json.RawMessage   `json:"manifests"`
}

func run(ctx context.Context, dir, out string) error {
	clock := time.Date(2026, 10, 12, 0, 0, 0, 0, time.UTC)
	book, err := open(ctx, filepath.Join(dir, "responder"), evidencebook.ResponderPolicy{}, &clock)
	if err != nil {
		return err
	}
	defer func() { _ = book.Release() }()
	var records []evidencebook.Record
	for i := range 3 {
		r, err := book.Append(ctx, evidencebook.Entry{RecordType: "observation", EpistemicType: evidencebook.ObservedEvent, SubjectRef: fmt.Sprintf("order-%d", i)})
		if err != nil {
			return err
		}
		records = append(records, r)
	}
	earlier, err := book.Checkpoint(ctx)
	if err != nil {
		return err
	}
	for i := 3; i < 7; i++ {
		r, err := book.Append(ctx, evidencebook.Entry{RecordType: "observation", EpistemicType: evidencebook.ObservedEvent, SubjectRef: fmt.Sprintf("order-%d", i)})
		if err != nil {
			return err
		}
		records = append(records, r)
	}
	anchor, err := book.Checkpoint(ctx)
	if err != nil {
		return err
	}
	pin := hexDigest(anchor.Statement)
	manifest := evidencebook.ManifestOptions{Bindings: []string{evidencebook.BindingStream, evidencebook.BindingHTTP}}
	first, err := book.Manifest(manifest)
	if err != nil {
		return err
	}
	subject := evidencebook.Subject{Kind: evidencebook.SubjectRecord, Digest: records[2].RecordID}
	request := func(s evidencebook.Subject, pin, nonce, profile, mediaType string) (evidencebook.Message, error) {
		octets, err := evidencebook.EncodeRequest(evidencebook.EvidenceRequest{Subject: s, Coverage: evidencebook.Coverage{ExpectedPin: pin}, Nonce: nonce, Profile: profile}, mediaType)
		return evidencebook.Message{Octets: octets, MediaType: mediaType}, err
	}
	respond := func(m evidencebook.Message, requester string) (evidencebook.Reply, error) {
		reply, _, err := book.Respond(ctx, m, evidencebook.RespondOptions{AuthenticatedRequester: requester, Policy: evidencebook.DefaultSharePolicy()})
		return reply, err
	}
	o := output{
		ResponderKey: hex.EncodeToString(seeded(responderLabel).Public().(ed25519.PublicKey)),
		Exchanges:    map[string]exchange{}, Invariance: map[string]string{}, RecordSet: map[string]string{}, Requests: map[string]carried{},
	}
	checkpoints, err := book.Checkpoints(ctx)
	if err != nil {
		return err
	}
	for _, cp := range checkpoints {
		o.LogKeys = append(o.LogKeys, map[string]string{"iss": cp.LogID, "sub": fmt.Sprintf("%s#%d", cp.LogID, cp.TreeSize), "kid": cp.KeyID})
	}

	// Request C (JSON) by alice and request D (CBOR, another nonce) by bob.
	reqC, err := request(subject, pin, "2026-10-12T00:00:10Z/c", evidencebook.ProfileID, evidencebook.MediaTypeJSON)
	if err != nil {
		return err
	}
	reqD, err := request(subject, pin, "2026-10-12T00:00:20Z/d", evidencebook.ProfileID, evidencebook.MediaTypeCBOR)
	if err != nil {
		return err
	}
	replyC, err := respond(reqC, "alice")
	if err != nil {
		return err
	}
	replyD, err := respond(reqD, "bob")
	if err != nil {
		return err
	}
	if replyC.Envelope == nil || replyD.Envelope == nil {
		return fmt.Errorf("requests C and D were refused")
	}
	o.Exchanges["valid-exchange"] = exchange{Requests: []carried{carry(reqC)}, Replies: []carried{carry(replyC.Message)}}
	o.Exchanges["invariance-two-nonces-two-transports"] = exchange{Requests: []carried{carry(reqC), carry(reqD)}, Replies: []carried{carry(replyC.Message), carry(replyD.Message)}}
	o.Invariance["alice-json-nonce-c"] = hex.EncodeToString(replyC.Envelope.Artifact)
	o.Invariance["bob-cbor-nonce-d"] = hex.EncodeToString(replyD.Envelope.Artifact)
	if err := os.WriteFile(filepath.Join(dir, "artifact-alice-json-nonce-c.bin"), replyC.Envelope.Artifact, 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "artifact-bob-cbor-nonce-d.bin"), replyD.Envelope.Artifact, 0o600); err != nil {
		return err
	}

	// The same two variations for a record-set subject, whose Artifact is
	// built by this responder rather than being a record's own octets.
	for _, v := range []struct{ name, nonce, mediaType, requester string }{
		{"full_history-alice-json-nonce-c", "2026-10-12T00:00:10Z/c", evidencebook.MediaTypeJSON, "alice"},
		{"full_history-bob-cbor-nonce-d", "2026-10-12T00:00:20Z/d", evidencebook.MediaTypeCBOR, "bob"},
	} {
		m, err := request(evidencebook.Subject{Kind: evidencebook.SubjectFullHistory}, pin, v.nonce, evidencebook.ProfileID, v.mediaType)
		if err != nil {
			return err
		}
		reply, err := respond(m, v.requester)
		if err != nil {
			return err
		}
		if reply.Envelope == nil {
			return fmt.Errorf("full_history request %s was refused", v.name)
		}
		o.RecordSet[v.name] = hex.EncodeToString(reply.Envelope.Artifact)
	}

	// A record this book does not hold: no_such_subject.
	missing, err := request(evidencebook.Subject{Kind: evidencebook.SubjectRecord, Digest: hexDigest([]byte("never recorded"))}, pin, "2026-10-12T00:00:10Z/c", evidencebook.ProfileID, evidencebook.MediaTypeJSON)
	if err != nil {
		return err
	}
	replyMissing, err := respond(missing, "alice")
	if err != nil {
		return err
	}
	o.Exchanges["missing-evidence-no-such-subject"] = exchange{Requests: []carried{carry(missing)}, Replies: []carried{carry(replyMissing.Message)}}

	// A profile this book does not implement: request_malformed.
	v9, err := request(subject, pin, "2026-10-12T00:00:40Z/v9", "evidence-interop/9", evidencebook.MediaTypeJSON)
	if err != nil {
		return err
	}
	replyV9, err := respond(v9, "alice")
	if err != nil {
		return err
	}
	o.Exchanges["unsupported-version-refused"] = exchange{Requests: []carried{carry(v9)}, Replies: []carried{carry(replyV9.Message)}}

	// The requests the envelope mutations are paired with: another record,
	// and the same record pinned at the earlier checkpoint.
	other, err := request(evidencebook.Subject{Kind: evidencebook.SubjectRecord, Digest: records[1].RecordID}, pin, "2026-10-12T00:00:10Z/c", evidencebook.ProfileID, evidencebook.MediaTypeJSON)
	if err != nil {
		return err
	}
	otherPin, err := request(subject, hexDigest(earlier.Statement), "2026-10-12T00:00:10Z/c", evidencebook.ProfileID, evidencebook.MediaTypeJSON)
	if err != nil {
		return err
	}
	o.Requests["another-record"], o.Requests["another-pin"] = carry(other), carry(otherPin)

	// The same request C to a responder whose cost policy declines it.
	declining, err := open(ctx, filepath.Join(dir, "declining"), evidencebook.ResponderPolicy{Limiter: limitNone{}}, &clock)
	if err != nil {
		return err
	}
	defer func() { _ = declining.Release() }()
	replyDeclined, _, err := declining.Respond(ctx, reqC, evidencebook.RespondOptions{AuthenticatedRequester: "alice", Policy: evidencebook.DefaultSharePolicy()})
	if err != nil {
		return err
	}
	o.Exchanges["declined"] = exchange{Requests: []carried{carry(reqC)}, Replies: []carried{carry(replyDeclined.Message)}}

	// A requester's own absence record: request C sent from another book,
	// and no reply in a one-minute window.
	requester, err := open(ctx, filepath.Join(dir, "requester"), evidencebook.ResponderPolicy{}, &clock)
	if err != nil {
		return err
	}
	defer func() { _ = requester.Release() }()
	sent, err := requester.Request(ctx, evidencebook.EvidenceRequest{Subject: subject, Coverage: evidencebook.Coverage{ExpectedPin: pin}, Nonce: "2026-10-12T00:00:10Z/c", Route: "peer:example-responder"}, "responder")
	if err != nil {
		return err
	}
	clock = clock.Add(2 * time.Minute)
	absence, err := requester.RecordAbsence(ctx, sent.RecordID, clock.Add(-time.Second), "")
	if err != nil {
		return err
	}
	absenceRecord := carry(evidencebook.Message{Octets: absence.Header.Statement, MediaType: evidencebook.MediaTypeJSON})
	o.Exchanges["missing-evidence-recorded-absence"] = exchange{Requests: []carried{carry(sent.Message())}, Replies: []carried{}, AbsenceRecord: &absenceRecord}

	// The manifest after every answer above is the one served before them:
	// it carries nothing that grows with history.
	last, err := book.Manifest(manifest)
	if err != nil {
		return err
	}
	o.Manifests = []json.RawMessage{first, last}
	data, err := json.MarshalIndent(o, "", " ")
	if err != nil {
		return err
	}
	return os.WriteFile(out, data, 0o600)
}

func hexDigest(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
