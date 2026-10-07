// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/action-state-group/agent-action-capsule/go/canonical"
)

// jsonNode is one value of a decoded JSON tree, read by exact key the way
// the neutral Evidence Bundle verifier reads it. encoding/json's struct
// decoding matches keys case-insensitively and lets the last duplicate win,
// so it could read a different member than the verifier checked; nothing
// read from a bundle goes through it.
type jsonNode struct {
	value any
}

func (n jsonNode) present() bool { return n.value != nil }

// has reports whether n is an object with key, whatever its value (null
// included), unlike get(key).present().
func (n jsonNode) has(key string) bool {
	m, ok := n.value.(map[string]any)
	if !ok {
		return false
	}
	_, ok = m[key]
	return ok
}

func (n jsonNode) isObject() bool {
	_, ok := n.value.(map[string]any)
	return ok
}

// get returns the member under exactly key; absent for any other value.
func (n jsonNode) get(key string) jsonNode {
	m, ok := n.value.(map[string]any)
	if !ok {
		return jsonNode{}
	}
	return jsonNode{value: m[key]}
}

func (n jsonNode) keys() []string {
	m, ok := n.value.(map[string]any)
	if !ok {
		return nil
	}
	out := make([]string, 0, len(m))
	for key := range m {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func (n jsonNode) items() []jsonNode {
	list, ok := n.value.([]any)
	if !ok {
		return nil
	}
	out := make([]jsonNode, len(list))
	for i, item := range list {
		out[i] = jsonNode{value: item}
	}
	return out
}

func (n jsonNode) str() (string, bool) {
	s, ok := n.value.(string)
	return s, ok
}

func (n jsonNode) uint() (uint64, bool) {
	number, ok := n.value.(json.Number)
	if !ok || canonical.IsFloat(number) || canonical.IsUnsafeInt(number) {
		return 0, false
	}
	value, err := number.Int64()
	if err != nil || value < 0 {
		return 0, false
	}
	return uint64(value), true
}

// canonical returns the JCS bytes of this subtree.
func (n jsonNode) canonical() (json.RawMessage, error) {
	return canonical.JCS(n.value)
}

// foldKey maps every rune to the smallest member of its Unicode simple-fold
// orbit, so two keys fold equal exactly when strings.EqualFold (and so
// encoding/json's key matching) treats them as equal.
func foldKey(key string) string {
	var b strings.Builder
	for _, r := range key {
		least := r
		for f := unicode.SimpleFold(r); f != r; f = unicode.SimpleFold(f) {
			least = min(least, f)
		}
		b.WriteRune(least)
	}
	return b.String()
}

// rejectFoldedKeys refuses any object, at any depth, holding two keys that
// differ only by case: one reader's "disclosures" is another's "Disclosures".
func rejectFoldedKeys(n jsonNode, path string) error {
	if n.isObject() {
		keys := n.keys()
		seen := make(map[string]string, len(keys))
		for _, key := range keys {
			folded := foldKey(key)
			if other, dup := seen[folded]; dup {
				return fmt.Errorf("%w: %s holds keys %q and %q that differ only by case", ErrInvalid, path, other, key)
			}
			seen[folded] = key
			if err := rejectFoldedKeys(n.get(key), path+"."+key); err != nil {
				return err
			}
		}
	}
	for i, item := range n.items() {
		if err := rejectFoldedKeys(item, fmt.Sprintf("%s[%d]", path, i)); err != nil {
			return err
		}
	}
	return nil
}
