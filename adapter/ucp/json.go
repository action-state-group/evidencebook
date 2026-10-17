// SPDX-License-Identifier: Apache-2.0

package ucp

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

// parseIJSON parses one JSON value as I-JSON (RFC 7493), the input RFC 8785
// requires: valid UTF-8, no escaped lone surrogate, no duplicate member name
// in any object, and nothing after the value. Numbers stay json.Number so the
// schema validator and the canonicalizer each read the text as written.
// encoding/json alone would replace a lone surrogate with U+FFFD and let the
// last duplicate win, so two readers of the same octets could sign or verify
// different content.
func parseIJSON(data []byte) (any, error) {
	if !utf8.Valid(data) {
		return nil, errors.New("not valid UTF-8")
	}
	if err := checkSurrogateEscapes(data); err != nil {
		return nil, err
	}
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
			o := map[string]any{}
			for dec.More() {
				kt, err := dec.Token()
				if err != nil {
					return nil, err
				}
				k, ok := kt.(string)
				if !ok {
					return nil, errors.New("object member name is not a string")
				}
				if _, dup := o[k]; dup {
					return nil, fmt.Errorf("duplicate member %q", k)
				}
				v, err := decodeValue(dec)
				if err != nil {
					return nil, err
				}
				o[k] = v
			}
			if _, err := dec.Token(); err != nil {
				return nil, err
			}
			return o, nil
		case '[':
			a := []any{}
			for dec.More() {
				v, err := decodeValue(dec)
				if err != nil {
					return nil, err
				}
				a = append(a, v)
			}
			if _, err := dec.Token(); err != nil {
				return nil, err
			}
			return a, nil
		}
		return nil, fmt.Errorf("unexpected %v", t)
	default:
		return t, nil
	}
}

// checkSurrogateEscapes rejects a \uD800-\uDFFF escape that is not half of a
// high-low pair. It only looks inside string literals; the decoder rejects
// any other malformed text afterwards.
func checkSurrogateEscapes(data []byte) error {
	inString := false
	for i := 0; i < len(data); i++ {
		c := data[i]
		if !inString {
			if c == '"' {
				inString = true
			}
			continue
		}
		switch c {
		case '"':
			inString = false
		case '\\':
			if i+1 >= len(data) {
				return nil
			}
			if data[i+1] != 'u' {
				i++
				continue
			}
			u, ok := hex4(data, i+2)
			if !ok {
				return nil // the decoder reports it
			}
			switch {
			case u >= 0xD800 && u <= 0xDBFF:
				if i+11 < len(data) && data[i+6] == '\\' && data[i+7] == 'u' {
					if l, ok := hex4(data, i+8); ok && l >= 0xDC00 && l <= 0xDFFF {
						i += 11
						continue
					}
				}
				return fmt.Errorf("lone high surrogate escape at offset %d", i)
			case u >= 0xDC00 && u <= 0xDFFF:
				return fmt.Errorf("lone low surrogate escape at offset %d", i)
			}
			i += 5
		}
	}
	return nil
}

func hex4(data []byte, at int) (rune, bool) {
	if at+4 > len(data) {
		return 0, false
	}
	n, err := strconv.ParseUint(string(data[at:at+4]), 16, 16)
	if err != nil {
		return 0, false
	}
	return rune(n), true
}

// jcs returns the RFC 8785 JSON Canonicalization Scheme form of a value
// returned by parseIJSON. It is a full RFC 8785 serializer: numbers are read
// as IEEE 754 doubles and written as ECMAScript Number.prototype.toString
// writes them (RFC 8785 §3.2.2.3), so 1.0 becomes 1 and 1e21 stays 1e+21.
//
// evidencebook's own canonicalJSON uses agent-action-capsule's integer-only
// profile of RFC 8785, which refuses a non-integer number. UCP signs
// arbitrary checkout content, so the adapter cannot refuse what RFC 8785
// accepts; for every input that profile accepts the two produce the same
// bytes (TestJCSAgreesWithCapsuleProfile).
func jcs(v any) ([]byte, error) {
	var b bytes.Buffer
	if err := writeJCS(&b, v); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

func writeJCS(b *bytes.Buffer, v any) error {
	switch t := v.(type) {
	case nil:
		b.WriteString("null")
	case bool:
		if t {
			b.WriteString("true")
		} else {
			b.WriteString("false")
		}
	case string:
		writeJCSString(b, t)
	case json.Number:
		s, err := jcsNumber(string(t))
		if err != nil {
			return err
		}
		b.WriteString(s)
	case []any:
		b.WriteByte('[')
		for i, e := range t {
			if i > 0 {
				b.WriteByte(',')
			}
			if err := writeJCS(b, e); err != nil {
				return err
			}
		}
		b.WriteByte(']')
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Slice(keys, func(i, j int) bool { return lessUTF16(keys[i], keys[j]) })
		b.WriteByte('{')
		for i, k := range keys {
			if i > 0 {
				b.WriteByte(',')
			}
			writeJCSString(b, k)
			b.WriteByte(':')
			if err := writeJCS(b, t[k]); err != nil {
				return err
			}
		}
		b.WriteByte('}')
	default:
		return fmt.Errorf("jcs: unsupported type %T", v)
	}
	return nil
}

// lessUTF16 orders member names by their UTF-16 code units (RFC 8785 §3.2.3).
func lessUTF16(a, b string) bool {
	au, bu := utf16.Encode([]rune(a)), utf16.Encode([]rune(b))
	for i := 0; i < len(au) && i < len(bu); i++ {
		if au[i] != bu[i] {
			return au[i] < bu[i]
		}
	}
	return len(au) < len(bu)
}

// writeJCSString writes a string as RFC 8785 §3.2.2.2 requires.
func writeJCSString(b *bytes.Buffer, s string) {
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\b':
			b.WriteString(`\b`)
		case '\t':
			b.WriteString(`\t`)
		case '\n':
			b.WriteString(`\n`)
		case '\f':
			b.WriteString(`\f`)
		case '\r':
			b.WriteString(`\r`)
		default:
			if r < 0x20 {
				fmt.Fprintf(b, `\u%04x`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
}

// jcsNumber serializes a JSON number as RFC 8785 §3.2.2.3 requires: parse
// it as an IEEE 754 double, then write the shortest round-tripping decimal
// in the ECMAScript Number.prototype.toString layout.
func jcsNumber(text string) (string, error) {
	f, err := strconv.ParseFloat(text, 64)
	if err != nil || math.IsInf(f, 0) || math.IsNaN(f) {
		return "", fmt.Errorf("number %s is not representable as an IEEE 754 double", text)
	}
	if f == 0 {
		return "0", nil // negative zero too
	}
	sign := ""
	if f < 0 {
		sign, f = "-", -f
	}
	format := byte('e')
	if f >= 1e-6 && f < 1e21 {
		format = 'f'
	}
	s := strconv.FormatFloat(f, format, -1, 64)
	if e := strings.IndexByte(s, 'e'); e > 0 {
		// Go writes e-07 and e+21; ECMAScript writes e-7 and e+21.
		mant, exp := s[:e], s[e+1:]
		expSign := exp[:1]
		digits := strings.TrimLeft(exp[1:], "0")
		s = mant + "e" + expSign + digits
	}
	return sign + s, nil
}
