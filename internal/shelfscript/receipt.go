// SPDX-License-Identifier: Apache-2.0

package shelfscript

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"

	"github.com/action-state-group/checkpointed-local-log/go/checkpoint"
	"github.com/fxamacker/cbor/v2"
	"github.com/veraison/go-cose"

	evidencebook "github.com/action-state-group/evidencebook"
)

// MintReceipt is the script's witness receipt for a cadence checkpoint: an
// RFC 9162 COSE receipt from WitnessKey over a one-entry log holding the
// checkpoint, so its inclusion path is empty. Ed25519 is deterministic, so
// the receipt's bytes depend on the statement alone; the vector carries them
// for implementations that do not mint receipts.
func MintReceipt(statement []byte) (evidencebook.WitnessReceipt, error) {
	record, err := checkpoint.ParseRecord(statement)
	if err != nil {
		return evidencebook.WitnessReceipt{}, err
	}
	entry, err := record.EntryHash()
	if err != nil {
		return evidencebook.WitnessReceipt{}, err
	}
	proof, err := cbor.Marshal([]any{int64(1), int64(0), [][]byte{}})
	if err != nil {
		return evidencebook.WitnessReceipt{}, err
	}
	leaf := sha256.Sum256(append([]byte{0}, entry...))
	message := cose.NewSign1Message()
	message.Headers.Protected.SetAlgorithm(cose.AlgorithmEdDSA)
	message.Headers.Protected[int64(395)] = int64(1)
	message.Headers.Unprotected[int64(396)] = map[any]any{int64(-1): []any{proof}}
	message.Payload = leaf[:]
	signer, err := cose.NewSigner(cose.AlgorithmEdDSA, WitnessKey)
	if err != nil {
		return evidencebook.WitnessReceipt{}, err
	}
	if err := message.Sign(rand.Reader, nil, signer); err != nil {
		return evidencebook.WitnessReceipt{}, err
	}
	message.Payload = nil
	raw, err := message.MarshalCBOR()
	if err != nil {
		return evidencebook.WitnessReceipt{}, err
	}
	return evidencebook.WitnessReceipt{TSURL: WitnessURL, EntryHash: hex.EncodeToString(entry), Receipt: raw, LeafIndex: 0, TreeSize: 1}, nil
}
