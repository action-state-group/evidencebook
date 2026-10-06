package evidencebook

import (
	"context"
	"encoding/json"
	"testing"
)

// The canonical JSON that request, refusal and artifact-response bytes use
// is RFC 8785 (JCS): its section 3.2.3 example string, with every escaping
// rule in it, comes out byte for byte, and members sort by key. Numbers
// follow agent-action-capsule's profile of JCS: integers within
// +/-(2^53-1) serialize as plain decimals; a float, or an integer beyond that
// range, is refused rather than written in a form another implementation
// could read differently.
func TestCanonicalJSONIsJCS(t *testing.T) {
	input := json.RawMessage(`{"numbers":[0,100,-1,9007199254740991],"string":"\u20ac$\u000F\u000aA'\u0042\u0022\u005c\\\"\/","literals":[null,true,false]}`)
	want := `{"literals":[null,true,false],"numbers":[0,100,-1,9007199254740991],"string":"€$\u000f\nA'B\"\\\\\"/"}`
	got, err := canonicalJSON(input)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Fatalf("not JCS:\n got  %s\n want %s", got, want)
	}
	// Characters encoding/json would escape for HTML stay as they are.
	got, err = canonicalJSON(map[string]string{"route": "a<b>&c"})
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"route":"a<b>&c"}`; string(got) != want {
		t.Fatalf("not JCS:\n got  %s\n want %s", got, want)
	}
	for _, refused := range []string{`[4.50]`, `[1E2]`, `[9007199254740993]`} {
		if _, err := canonicalJSON(json.RawMessage(refused)); err == nil {
			t.Errorf("%s: a number outside the integer profile must be refused", refused)
		}
	}
}

// What a requester records is the bytes it transmits: the request read back
// from its record has the transmitted bytes and digest, even when a member
// holds characters encoding/json escapes for HTML.
func TestRecordedRequestIsTheTransmittedBytes(t *testing.T) {
	book := newFixture(t, "requester", 30).open(t)
	sent, err := book.Request(context.Background(), EvidenceRequest{Subject: Subject{Kind: SubjectFullHistory}, Coverage: fresh(), Route: "https://a.example/?x=<1>&y=2"}, "book-a")
	if err != nil {
		t.Fatal(err)
	}
	recorded, err := book.sentLocked(sent.RecordID)
	if err != nil {
		t.Fatal(err)
	}
	if string(recorded.bytes) != string(sent.Bytes) || recorded.digest != sent.Digest {
		t.Fatalf("recorded request differs from the transmitted one:\n sent     %s\n recorded %s", sent.Bytes, recorded.bytes)
	}
}
