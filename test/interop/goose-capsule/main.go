// SPDX-License-Identifier: Apache-2.0

// Command goose-capsule is Go's check of a Capsule the Goose reference
// (python/examples/goose) sealed with loop_seal: Class 1 verification, the
// producer envelope under the key_id it carries, and the effect and model
// members the loop's step 01 reads. It prints one JSON object and exits 0
// only when the Capsule is accepted.
//
//	goose-capsule CAPSULE.json
package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"

	emit "github.com/action-state-group/capsule-emit/go"
)

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: goose-capsule CAPSULE.json")
		os.Exit(2)
	}
	out, err := check(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	enc, _ := json.Marshal(out)
	fmt.Println(string(enc))
}

func check(path string) (map[string]any, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var capsule map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&capsule); err != nil {
		return nil, fmt.Errorf("not a JSON object: %w", err)
	}
	signature, _ := capsule["signature"].(string)
	keyID, _ := capsule["key_id"].(string)
	capsuleID, _ := capsule["capsule_id"].(string)
	// The envelope fields sit outside the Capsule ID's preimage.
	delete(capsule, "signature")
	delete(capsule, "key_id")
	// No HTML escaping, as JCS writes it.
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(capsule); err != nil {
		return nil, err
	}
	body := bytes.TrimSpace(buf.Bytes())
	if _, err := emit.VerifyCapsule(body); err != nil {
		return nil, fmt.Errorf("class 1: %w", err)
	}
	envelope, err := hex.DecodeString(signature)
	if err != nil || signature == "" {
		return nil, fmt.Errorf("the Capsule carries no producer signature")
	}
	verified, err := emit.VerifyEnvelope(capsuleID, envelope)
	if err != nil {
		return nil, err
	}
	if hex.EncodeToString(verified.PublicKey) != keyID {
		return nil, fmt.Errorf("signed by %x, not the key_id %s it carries", verified.PublicKey, keyID)
	}
	effect, _ := capsule["effect"].(map[string]any)
	model, _ := capsule["model_attestation"].(map[string]any)
	if effect["effect_attestation"] != "runtime_claimed" {
		return nil, fmt.Errorf("effect_attestation %v, want runtime_claimed", effect["effect_attestation"])
	}
	if model["provider"] == nil || model["model_id"] == nil {
		return nil, fmt.Errorf("model_attestation names no provider and model_id")
	}
	return map[string]any{"accepted": true, "capsule_id": capsuleID, "key_id": keyID,
		"effect_attestation": effect["effect_attestation"],
		"provider":           model["provider"], "model_id": model["model_id"]}, nil
}
