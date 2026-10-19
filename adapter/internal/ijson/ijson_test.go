// SPDX-License-Identifier: Apache-2.0

package ijson

import (
	"encoding/binary"
	"encoding/hex"
	"math"
	"strconv"
	"strings"
	"testing"
)

// RFC 8785 §3.2.2 sample, and its §3.2.4 UTF-8 output.
func TestJCSRFC8785Sample(t *testing.T) {
	in := `{
       "numbers": [333333333.33333329, 1E30, 4.50,
                   2e-3, 0.000000000000000000000000001],
       "string": "\u20ac$\u000F\u000aA'\u0042\u0022\u005c\\\"\/",
       "literals": [null, true, false]
     }`
	wantHex := strings.Join(strings.Fields(`
     7b 22 6c 69 74 65 72 61 6c 73 22 3a 5b 6e 75 6c 6c 2c 74 72
     75 65 2c 66 61 6c 73 65 5d 2c 22 6e 75 6d 62 65 72 73 22 3a
     5b 33 33 33 33 33 33 33 33 33 2e 33 33 33 33 33 33 33 2c 31
     65 2b 33 30 2c 34 2e 35 2c 30 2e 30 30 32 2c 31 65 2d 32 37
     5d 2c 22 73 74 72 69 6e 67 22 3a 22 e2 82 ac 24 5c 75 30 30
     30 66 5c 6e 41 27 42 5c 22 5c 5c 5c 5c 5c 22 2f 22 7d`), "")
	v, err := Parse([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	got, err := JCS(v)
	if err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(got) != wantHex {
		t.Fatalf("got %s", got)
	}
}

// RFC 8785 §3.2.3 sorting sample: names sort by UTF-16 code units.
func TestJCSRFC8785Sorting(t *testing.T) {
	in := `{"\u20ac":"Euro Sign","\r":"Carriage Return","\ufb33":"Hebrew Letter Dalet With Dagesh","1":"One","\ud83d\ude00":"Emoji: Grinning Face","\u0080":"Control","\u00f6":"Latin Small Letter O With Diaeresis"}`
	v, err := Parse([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	got, err := JCS(v)
	if err != nil {
		t.Fatal(err)
	}
	order := []string{"Carriage Return", "One", "Control", "Latin Small Letter O With Diaeresis", "Euro Sign", "Emoji: Grinning Face", "Hebrew Letter Dalet With Dagesh"}
	last := -1
	for _, w := range order {
		i := strings.Index(string(got), w)
		if i <= last {
			t.Fatalf("order wrong at %q: %s", w, got)
		}
		last = i
	}
}

// RFC 8785 Appendix B, Table 1.
func TestJCSNumberSamples(t *testing.T) {
	cases := map[string]string{
		"0000000000000000": "0",
		"8000000000000000": "0",
		"0000000000000001": "5e-324",
		"8000000000000001": "-5e-324",
		"7fefffffffffffff": "1.7976931348623157e+308",
		"ffefffffffffffff": "-1.7976931348623157e+308",
		"4340000000000000": "9007199254740992",
		"c340000000000000": "-9007199254740992",
		"4430000000000000": "295147905179352830000",
		"44b52d02c7e14af5": "9.999999999999997e+22",
		"44b52d02c7e14af6": "1e+23",
		"44b52d02c7e14af7": "1.0000000000000001e+23",
		"444b1ae4d6e2ef4e": "999999999999999700000",
		"444b1ae4d6e2ef4f": "999999999999999900000",
		"444b1ae4d6e2ef50": "1e+21",
		"3eb0c6f7a0b5ed8c": "9.999999999999997e-7",
		"3eb0c6f7a0b5ed8d": "0.000001",
		"41b3de4355555553": "333333333.3333332",
		"41b3de4355555554": "333333333.33333325",
		"41b3de4355555555": "333333333.3333333",
		"41b3de4355555556": "333333333.3333334",
		"41b3de4355555557": "333333333.33333343",
		"becbf647612f3696": "-0.0000033333333333333333",
		"43143ff3c1cb0959": "1424953923781206.2",
	}
	for bits, want := range cases {
		b, _ := hex.DecodeString(bits)
		f := math.Float64frombits(binary.BigEndian.Uint64(b))
		got, err := jcsNumber(strconv.FormatFloat(f, 'g', -1, 64))
		if err != nil || got != want {
			t.Errorf("%s: got %q (%v), want %q", bits, got, err, want)
		}
	}
	for _, bad := range []string{"1e400", "-1e400"} {
		if _, err := jcsNumber(bad); err == nil {
			t.Errorf("%s: want an error (not an IEEE 754 double)", bad)
		}
	}
}

func TestParseRejects(t *testing.T) {
	for name, in := range map[string]string{
		"duplicate member":      `{"a":1,"a":2}`,
		"nested duplicate":      `{"a":{"b":1,"b":1}}`,
		"lone high surrogate":   `{"a":"\ud800"}`,
		"lone low surrogate":    `{"a":"\udc00x"}`,
		"high then non-low":     `{"a":"\ud800\u0041"}`,
		"invalid UTF-8":         "{\"a\":\"\xff\"}",
		"trailing data":         `{"a":1} {}`,
		"not JSON":              `{"a":}`,
		"lone surrogate in key": `{"\udfff":1}`,
	} {
		if _, err := Parse([]byte(in)); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	for _, ok := range []string{`{"a":"\ud83d\ude00"}`, `{"a":"\\ud800"}`, `["\"\\"]`} {
		if _, err := Parse([]byte(ok)); err != nil {
			t.Errorf("%s: %v", ok, err)
		}
	}
}
