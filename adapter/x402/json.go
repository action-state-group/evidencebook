// SPDX-License-Identifier: Apache-2.0

package x402

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// decodeStrict parses one JSON value into maps, slices, strings, bools, nil
// and json.Number, rejecting a duplicate member name in any object and any
// trailing data. encoding/json's struct decoding lets the last duplicate win
// and matches names case-insensitively, so two readers of the same octets
// could read different values; this reader leaves no such choice.
func decodeStrict(data []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	v, err := decodeValue(dec)
	if err != nil {
		return nil, err
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("trailing data after the JSON value")
	}
	return v, nil
}

func decodeValue(dec *json.Decoder) (any, error) {
	tok, err := dec.Token()
	if err != nil {
		return nil, err
	}
	switch t := tok.(type) {
	case json.Delim:
		switch t {
		case '{':
			obj := map[string]any{}
			for dec.More() {
				kt, err := dec.Token()
				if err != nil {
					return nil, err
				}
				k, ok := kt.(string)
				if !ok {
					return nil, errors.New("object member name is not a string")
				}
				if _, dup := obj[k]; dup {
					return nil, fmt.Errorf("duplicate member %q", k)
				}
				v, err := decodeValue(dec)
				if err != nil {
					return nil, err
				}
				obj[k] = v
			}
			if _, err := dec.Token(); err != nil {
				return nil, err
			}
			return obj, nil
		case '[':
			arr := []any{}
			for dec.More() {
				v, err := decodeValue(dec)
				if err != nil {
					return nil, err
				}
				arr = append(arr, v)
			}
			if _, err := dec.Token(); err != nil {
				return nil, err
			}
			return arr, nil
		}
		return nil, fmt.Errorf("unexpected %v", t)
	default:
		return t, nil
	}
}

// obj is a decoded JSON object read by exact member name.
type obj map[string]any

func asObj(v any) (obj, bool) {
	m, ok := v.(map[string]any)
	return obj(m), ok
}

func (o obj) has(k string) bool { _, ok := o[k]; return ok }

func (o obj) str(k string) (string, bool) {
	s, ok := o[k].(string)
	return s, ok
}

func (o obj) object(k string) (obj, bool) { return asObj(o[k]) }

func (o obj) num(k string) (json.Number, bool) {
	n, ok := o[k].(json.Number)
	return n, ok
}

func (o obj) boolean(k string) (bool, bool) {
	b, ok := o[k].(bool)
	return b, ok
}
