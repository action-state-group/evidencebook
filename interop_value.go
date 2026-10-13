// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/action-state-group/agent-action-capsule/go/canonical"
	"github.com/fxamacker/cbor/v2"
)

// The two encodings a request and its reply travel in (profile §4, §11.4.1
// rule 7). Media types are compared as exact strings.
const (
	MediaTypeJSON = "application/json"
	MediaTypeCBOR = "application/cbor"
)

// Message is octets as carried, with the media type they were carried under.
// A request digest is always over Octets exactly as received.
type Message struct {
	Octets    []byte
	MediaType string
}

// maxNesting bounds how deep a received JSON or CBOR value may nest. No
// object the profile defines nests deeper than a handful of levels.
const maxNesting = 32

// errNotWellFormed marks a received map that is not well formed (§5.2): a
// duplicate member, trailing bytes, invalid UTF-8, or a value outside the
// data model.
var errNotWellFormed = errors.New("not well formed")

// jsonNumber is a JSON number token as received. Whether it is a value the
// profile allows (an unsigned integer up to 2^53-1) is decided where a
// defined member is read; an unknown member may hold any number.
type jsonNumber string

// outsideModel is a received item the profile's data model has no place for
// (a negative or out-of-range integer, a float, a tag, a non-text map key).
// It is kept, not rejected, so an unknown member holding one is ignored.
type outsideModel struct{}

// maxSafeInteger is 2^53-1, the largest integer every JCS implementation
// represents exactly (§5.2).
const maxSafeInteger = 1<<53 - 1

var unsignedText = regexp.MustCompile(`^(0|[1-9][0-9]{0,15})$`)

// uintValue reads v as an unsigned integer in [0, 2^53-1].
func uintValue(v any) (uint64, bool) {
	switch n := v.(type) {
	case uint64:
		return n, n <= maxSafeInteger
	case jsonNumber:
		if !unsignedText.MatchString(string(n)) {
			return 0, false
		}
		parsed, err := strconv.ParseUint(string(n), 10, 64)
		return parsed, err == nil && parsed <= maxSafeInteger
	}
	return 0, false
}

// decodeMessage decodes a received object in the encoding its media type
// names. Duplicate members, trailing bytes and invalid UTF-8 are errors.
func decodeMessage(m Message) (any, error) {
	switch m.MediaType {
	case MediaTypeJSON:
		return decodeStrictJSON(m.Octets)
	case MediaTypeCBOR:
		return decodeStrictCBOR(m.Octets)
	}
	return nil, fmt.Errorf("%w: media type %q is neither %s nor %s", ErrInvalid, m.MediaType, MediaTypeJSON, MediaTypeCBOR)
}

// decodeStrictJSON decodes one JSON value. Unlike encoding/json it refuses a
// duplicate member (§5.2), matches member names exactly (no case folding),
// and keeps numbers as their token text.
func decodeStrictJSON(data []byte) (any, error) {
	if !utf8.Valid(data) {
		return nil, fmt.Errorf("%w: invalid UTF-8", errNotWellFormed)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	value, err := readJSONValue(decoder, 0)
	if err != nil {
		return nil, err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return nil, fmt.Errorf("%w: trailing data after the JSON value", errNotWellFormed)
	}
	return value, nil
}

func readJSONValue(decoder *json.Decoder, depth int) (any, error) {
	if depth > maxNesting {
		return nil, fmt.Errorf("%w: nested too deeply", errNotWellFormed)
	}
	token, err := decoder.Token()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errNotWellFormed, err)
	}
	switch t := token.(type) {
	case json.Delim:
		switch t {
		case '{':
			object := map[string]any{}
			for decoder.More() {
				keyToken, err := decoder.Token()
				if err != nil {
					return nil, fmt.Errorf("%w: %v", errNotWellFormed, err)
				}
				key, ok := keyToken.(string)
				if !ok {
					return nil, fmt.Errorf("%w: member name is not a string", errNotWellFormed)
				}
				if _, duplicate := object[key]; duplicate {
					return nil, fmt.Errorf("%w: duplicate member %q", errNotWellFormed, key)
				}
				if object[key], err = readJSONValue(decoder, depth+1); err != nil {
					return nil, err
				}
			}
			if _, err := decoder.Token(); err != nil {
				return nil, fmt.Errorf("%w: %v", errNotWellFormed, err)
			}
			return object, nil
		case '[':
			array := []any{}
			for decoder.More() {
				item, err := readJSONValue(decoder, depth+1)
				if err != nil {
					return nil, err
				}
				array = append(array, item)
			}
			if _, err := decoder.Token(); err != nil {
				return nil, fmt.Errorf("%w: %v", errNotWellFormed, err)
			}
			return array, nil
		}
		return nil, fmt.Errorf("%w: unexpected %v", errNotWellFormed, t)
	case json.Number:
		return jsonNumber(t), nil
	case string, bool, nil:
		return t, nil
	}
	return nil, fmt.Errorf("%w: unexpected token %v", errNotWellFormed, token)
}

var strictCBOR = func() cbor.DecMode {
	mode, err := cbor.DecOptions{
		DupMapKey:         cbor.DupMapKeyEnforcedAPF,
		MaxNestedLevels:   maxNesting,
		UTF8:              cbor.UTF8RejectInvalid,
		IndefLength:       cbor.IndefLengthAllowed,
		TagsMd:            cbor.TagsAllowed,
		IntDec:            cbor.IntDecConvertNone,
		ExtraReturnErrors: cbor.ExtraDecErrorNone,
	}.DecMode()
	if err != nil {
		panic(err)
	}
	return mode
}()

// decodeStrictCBOR decodes one CBOR data item into the profile's data model.
// It need not be deterministic: a request digest is over the octets as
// received, never over a re-encoding (ERV digest.json).
func decodeStrictCBOR(data []byte) (any, error) {
	var raw any
	if err := strictCBOR.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("%w: %v", errNotWellFormed, err)
	}
	return fromCBOR(raw), nil
}

func fromCBOR(v any) any {
	switch x := v.(type) {
	case nil, bool, string, []byte:
		return x
	case uint64:
		if x > maxSafeInteger {
			return outsideModel{}
		}
		return x
	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			out[i] = fromCBOR(item)
		}
		return out
	case map[any]any:
		out := make(map[string]any, len(x))
		for key, item := range x {
			name, ok := key.(string)
			if !ok {
				return outsideModel{}
			}
			out[name] = fromCBOR(item)
		}
		return out
	}
	return outsideModel{}
}

// toJCS serializes a profile value (map[string]any, []any, string, uint64,
// []byte as base64url without padding, nil) as UTF-8 of RFC 8785 JCS.
func toJCS(v any) ([]byte, error) {
	converted, err := jsonModel(v)
	if err != nil {
		return nil, err
	}
	return canonical.JCS(converted)
}

func jsonModel(v any) (any, error) {
	switch x := v.(type) {
	case nil, bool, string:
		return x, nil
	case uint64:
		if x > maxSafeInteger {
			return nil, fmt.Errorf("%w: integer %d is above 2^53-1", ErrInvalid, x)
		}
		return json.Number(strconv.FormatUint(x, 10)), nil
	case jsonNumber:
		return json.Number(x), nil
	case []byte:
		return base64.RawURLEncoding.EncodeToString(x), nil
	case []any:
		out := make([]any, len(x))
		for i, item := range x {
			converted, err := jsonModel(item)
			if err != nil {
				return nil, err
			}
			out[i] = converted
		}
		return out, nil
	case map[string]any:
		out := make(map[string]any, len(x))
		for key, item := range x {
			converted, err := jsonModel(item)
			if err != nil {
				return nil, err
			}
			out[key] = converted
		}
		return out, nil
	}
	return nil, fmt.Errorf("%w: %T has no place in the profile's data model", ErrInvalid, v)
}

var deterministicCBOR = func() cbor.EncMode {
	mode, err := cbor.CoreDetEncOptions().EncMode()
	if err != nil {
		panic(err)
	}
	return mode
}()

// toDeterministicCBOR serializes a profile value as deterministic CBOR (RFC
// 8949 §4.2.1); []byte is a byte string.
func toDeterministicCBOR(v any) ([]byte, error) {
	if err := cborModel(v); err != nil {
		return nil, err
	}
	return deterministicCBOR.Marshal(v)
}

func cborModel(v any) error {
	switch x := v.(type) {
	case nil, bool, string, []byte:
		return nil
	case uint64:
		if x > maxSafeInteger {
			return fmt.Errorf("%w: integer %d is above 2^53-1", ErrInvalid, x)
		}
		return nil
	case []any:
		for _, item := range x {
			if err := cborModel(item); err != nil {
				return err
			}
		}
		return nil
	case map[string]any:
		for _, item := range x {
			if err := cborModel(item); err != nil {
				return err
			}
		}
		return nil
	}
	return fmt.Errorf("%w: %T has no place in the profile's data model", ErrInvalid, v)
}

// encodeIn serializes v in the encoding mediaType names: JCS for JSON,
// deterministic CBOR for CBOR.
func encodeIn(mediaType string, v any) ([]byte, error) {
	switch mediaType {
	case MediaTypeJSON:
		return toJCS(v)
	case MediaTypeCBOR:
		return toDeterministicCBOR(v)
	}
	return nil, fmt.Errorf("%w: media type %q", ErrInvalid, mediaType)
}

// opaqueBytes reads an opaque byte value: a byte string in CBOR, base64url
// without padding in JSON (§4).
func opaqueBytes(v any, mediaType string) ([]byte, bool) {
	switch x := v.(type) {
	case []byte:
		return x, mediaType == MediaTypeCBOR
	case string:
		if mediaType != MediaTypeJSON {
			return nil, false
		}
		decoded, err := base64.RawURLEncoding.Strict().DecodeString(x)
		return decoded, err == nil
	}
	return nil, false
}

// profileTimeLayout is the §5.2 time form: RFC 3339 UTC, seconds precision.
const profileTimeLayout = "2006-01-02T15:04:05Z"

// profileTime formats t in the §5.2 form, dropping any fraction.
func profileTime(t time.Time) string {
	return t.UTC().Truncate(time.Second).Format(profileTimeLayout)
}

var profileTimeForm = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}Z$`)

// parseProfileTime reads a time in exactly the §5.2 form.
func parseProfileTime(s string) (time.Time, bool) {
	if !profileTimeForm.MatchString(s) {
		return time.Time{}, false
	}
	t, err := time.Parse(profileTimeLayout, s)
	return t, err == nil
}

var rfc3339UTC = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2}(\.[0-9]+)?Z$`)

// parseRequestTime reads a time a request carries (deadline, min_freshness):
// an RFC 3339 UTC time (ERV A3, A5). The §5.2 seconds form is what a
// requester following the profile sends; a fraction from a requester
// following the request draft alone is accepted.
func parseRequestTime(s string) (time.Time, bool) {
	if !rfc3339UTC.MatchString(s) {
		return time.Time{}, false
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	return t, err == nil
}
