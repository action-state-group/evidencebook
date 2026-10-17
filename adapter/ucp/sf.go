// SPDX-License-Identifier: Apache-2.0

package ucp

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// A minimal RFC 8941 Structured Field parser: Dictionaries whose members are
// Items or Inner Lists, with Parameters; bare items of type Integer, String,
// Token, Byte Sequence and Boolean. Decimals are not used by RFC 9421,
// RFC 9530 or UCP's headers and are refused.

type sfParam struct {
	key string
	val any // int64, string, sfToken, []byte, bool
}

type sfToken string

type sfItem struct {
	val    any
	params []sfParam
}

type sfMember struct {
	key    string
	item   *sfItem   // set for an Item member
	inner  []sfItem  // set for an Inner List member
	params []sfParam // the Inner List's parameters
	isList bool
}

func (m sfMember) param(name string) (any, bool) {
	ps := m.params
	if !m.isList && m.item != nil {
		ps = m.item.params
	}
	for _, p := range ps {
		if p.key == name {
			return p.val, true
		}
	}
	return nil, false
}

type sfParser struct {
	s string
	i int
}

func (p *sfParser) peek() byte {
	if p.i < len(p.s) {
		return p.s[p.i]
	}
	return 0
}

func (p *sfParser) eof() bool { return p.i >= len(p.s) }

func (p *sfParser) skipSP() {
	for p.peek() == ' ' {
		p.i++
	}
}

func (p *sfParser) skipOWS() {
	for c := p.peek(); c == ' ' || c == '\t'; c = p.peek() {
		p.i++
	}
}

// parseSFDictionary parses a Dictionary field value (RFC 8941 §4.2.2). A
// repeated key keeps its last value, as the RFC says, and its first position.
func parseSFDictionary(s string) ([]sfMember, error) {
	p := &sfParser{s: strings.Trim(s, " ")}
	var out []sfMember
	for !p.eof() {
		key, err := p.key()
		if err != nil {
			return nil, err
		}
		m := sfMember{key: key}
		if p.peek() == '=' {
			p.i++
			if p.peek() == '(' {
				m.isList = true
				if m.inner, m.params, err = p.innerList(); err != nil {
					return nil, err
				}
			} else {
				it, err := p.item()
				if err != nil {
					return nil, err
				}
				m.item = &it
			}
		} else {
			params, err := p.params()
			if err != nil {
				return nil, err
			}
			m.item = &sfItem{val: true, params: params}
		}
		replaced := false
		for j := range out {
			if out[j].key == key {
				out[j], replaced = m, true
			}
		}
		if !replaced {
			out = append(out, m)
		}
		p.skipOWS()
		if p.eof() {
			break
		}
		if p.peek() != ',' {
			return nil, fmt.Errorf("expected ',' at %d", p.i)
		}
		p.i++
		p.skipOWS()
		if p.eof() {
			return nil, errors.New("trailing ','")
		}
	}
	return out, nil
}

func (p *sfParser) key() (string, error) {
	start := p.i
	c := p.peek()
	if !(c >= 'a' && c <= 'z') && c != '*' {
		return "", fmt.Errorf("bad key at %d", p.i)
	}
	for c := p.peek(); (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '_' || c == '-' || c == '.' || c == '*'; c = p.peek() {
		p.i++
	}
	return p.s[start:p.i], nil
}

func (p *sfParser) innerList() ([]sfItem, []sfParam, error) {
	p.i++ // (
	var items []sfItem
	for !p.eof() {
		p.skipSP()
		if p.peek() == ')' {
			p.i++
			params, err := p.params()
			return items, params, err
		}
		it, err := p.item()
		if err != nil {
			return nil, nil, err
		}
		items = append(items, it)
		if c := p.peek(); c != ' ' && c != ')' {
			return nil, nil, fmt.Errorf("bad inner list at %d", p.i)
		}
	}
	return nil, nil, errors.New("unterminated inner list")
}

func (p *sfParser) item() (sfItem, error) {
	v, err := p.bare()
	if err != nil {
		return sfItem{}, err
	}
	params, err := p.params()
	return sfItem{val: v, params: params}, err
}

func (p *sfParser) params() ([]sfParam, error) {
	var out []sfParam
	for p.peek() == ';' {
		p.i++
		p.skipSP()
		k, err := p.key()
		if err != nil {
			return nil, err
		}
		var v any = true
		if p.peek() == '=' {
			p.i++
			if v, err = p.bare(); err != nil {
				return nil, err
			}
		}
		replaced := false
		for j := range out {
			if out[j].key == k {
				out[j].val, replaced = v, true
			}
		}
		if !replaced {
			out = append(out, sfParam{k, v})
		}
	}
	return out, nil
}

func (p *sfParser) bare() (any, error) {
	c := p.peek()
	switch {
	case c == '-' || (c >= '0' && c <= '9'):
		start := p.i
		if c == '-' {
			p.i++
		}
		for c := p.peek(); c >= '0' && c <= '9'; c = p.peek() {
			p.i++
		}
		if p.peek() == '.' {
			return nil, errors.New("decimals are not supported")
		}
		digits := strings.TrimPrefix(p.s[start:p.i], "-")
		if len(digits) == 0 || len(digits) > 15 {
			return nil, fmt.Errorf("bad integer at %d", start)
		}
		n, err := strconv.ParseInt(p.s[start:p.i], 10, 64)
		return n, err
	case c == '"':
		p.i++
		var b strings.Builder
		for !p.eof() {
			c := p.s[p.i]
			p.i++
			switch {
			case c == '\\':
				if p.eof() || (p.s[p.i] != '"' && p.s[p.i] != '\\') {
					return nil, errors.New("bad escape in string")
				}
				b.WriteByte(p.s[p.i])
				p.i++
			case c == '"':
				return b.String(), nil
			case c < 0x20 || c > 0x7e:
				return nil, errors.New("non-ASCII or control character in string")
			default:
				b.WriteByte(c)
			}
		}
		return nil, errors.New("unterminated string")
	case c == ':':
		p.i++
		end := strings.IndexByte(p.s[p.i:], ':')
		if end < 0 {
			return nil, errors.New("unterminated byte sequence")
		}
		enc := p.s[p.i : p.i+end]
		p.i += end + 1
		b, err := base64.StdEncoding.DecodeString(enc)
		if err != nil {
			return nil, errors.New("byte sequence is not base64")
		}
		return b, nil
	case c == '?':
		p.i++
		switch p.peek() {
		case '1':
			p.i++
			return true, nil
		case '0':
			p.i++
			return false, nil
		}
		return nil, errors.New("bad boolean")
	case (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || c == '*':
		start := p.i
		for !p.eof() {
			c := p.s[p.i]
			if c <= 0x20 || c >= 0x7f || strings.IndexByte(`"(),;<=>?@[\]{}`, c) >= 0 {
				break
			}
			p.i++
		}
		return sfToken(p.s[start:p.i]), nil
	}
	return nil, fmt.Errorf("unexpected %q at %d", c, p.i)
}

// serializeInnerList writes an Inner List with its parameters (RFC 8941
// §4.1.1.1), as RFC 9421 §2.3 writes @signature-params.
func serializeInnerList(items []sfItem, params []sfParam) (string, error) {
	var b strings.Builder
	b.WriteByte('(')
	for i, it := range items {
		if i > 0 {
			b.WriteByte(' ')
		}
		s, err := serializeBare(it.val)
		if err != nil {
			return "", err
		}
		b.WriteString(s)
		if err := writeParams(&b, it.params); err != nil {
			return "", err
		}
	}
	b.WriteByte(')')
	err := writeParams(&b, params)
	return b.String(), err
}

func writeParams(b *strings.Builder, params []sfParam) error {
	for _, p := range params {
		b.WriteByte(';')
		b.WriteString(p.key)
		if v, ok := p.val.(bool); ok && v {
			continue
		}
		s, err := serializeBare(p.val)
		if err != nil {
			return err
		}
		b.WriteByte('=')
		b.WriteString(s)
	}
	return nil
}

func serializeBare(v any) (string, error) {
	switch t := v.(type) {
	case int64:
		return strconv.FormatInt(t, 10), nil
	case string:
		return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(t) + `"`, nil
	case sfToken:
		return string(t), nil
	case []byte:
		return ":" + base64.StdEncoding.EncodeToString(t) + ":", nil
	case bool:
		if t {
			return "?1", nil
		}
		return "?0", nil
	}
	return "", fmt.Errorf("cannot serialize %T", v)
}
