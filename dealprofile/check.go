// SPDX-License-Identifier: Apache-2.0

package dealprofile

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/action-state-group/agent-action-capsule/go/canonical"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
)

// The record-level stages a record must pass, in the order the reference
// checker (x-deal-v0/check_profile.py, check_record) runs them, and the
// digest stage a claimed digest is checked at. The chain stage, which reads
// a record against the records before it, is not here yet.
const (
	StageCanonicalization = "canonicalization"
	StagePersonalData     = "personal_data"
	StageWording          = "wording"
	StageSchema           = "schema"
	StageDigest           = "digest"
)

// StageError is a record refused at one stage.
type StageError struct {
	Stage  string
	Detail string
}

func (e *StageError) Error() string { return "[" + e.Stage + "] " + e.Detail }

func stageErr(stage, format string, args ...any) *StageError {
	return &StageError{Stage: stage, Detail: fmt.Sprintf(format, args...)}
}

// typedActionTypes are the typed records a chain may hold (PROFILE.md §9),
// and the x-deal-v0 record type each stands for.
var typedActionTypes = map[string]string{
	TypeTaskAuthority: "task_authority", TypeProposedAction: TypeCheck, TypeActionEvaluation: TypeVerdict,
	TypeActionApproval: TypeApproval, TypeActionRecord: TypeAction, TypeActionOutcome: TypeOutcome, TypeActionReport: "report",
}

// Encode is a record's payload, the JCS bytes a step seals, and its digest,
// once the record passes Check. record is any value that marshals to the
// record's JSON. localValues are the raw local-store values the record must
// not carry (every identifier seen in the deal, and every committed text
// long enough not to collide with a token); they are used only for that
// scan and never sealed.
func Encode(record any, localValues []string) (payload []byte, digest string, err error) {
	doc, err := decode(record)
	if err != nil {
		return nil, "", err
	}
	if err = checkDoc(doc, localValues); err != nil {
		return nil, "", err
	}
	if payload, err = canonical.JCS(doc); err != nil {
		return nil, "", err
	}
	digest, err = canonical.JSONDigest(doc)
	return payload, digest, err
}

// Check runs the record-level stages over record (canonicalization,
// personal_data, wording, schema) and returns the first refusal as a
// *StageError.
func Check(record any, localValues []string) error {
	doc, err := decode(record)
	if err != nil {
		return err
	}
	return checkDoc(doc, localValues)
}

// Digest is a record's digest: lowercase hex SHA-256 over its JCS bytes
// (PROFILE.md §5).
func Digest(record any) (string, error) {
	doc, err := decode(record)
	if err != nil {
		return "", err
	}
	return canonical.JSONDigest(doc)
}

// CheckDigest refuses, at the digest stage, a claimed digest that is not
// the record's.
func CheckDigest(record any, claimed string) error {
	d, err := Digest(record)
	if err != nil {
		return err
	}
	if d != claimed {
		return stageErr(StageDigest, "claimed digest is not SHA-256 over the JCS bytes")
	}
	return nil
}

// decode reads record as generic JSON with numbers kept as written
// (json.Number), as a verifier reads the sealed bytes.
func decode(record any) (any, error) {
	var raw []byte
	switch r := record.(type) {
	case []byte:
		raw = r
	case json.RawMessage:
		raw = r
	default:
		var err error
		if raw, err = json.Marshal(record); err != nil {
			return nil, err
		}
	}
	return jsonschema.UnmarshalJSON(bytes.NewReader(raw))
}

func checkDoc(doc any, localValues []string) error {
	if err := stageCanonicalization(doc); err != nil {
		return err
	}
	if err := stagePersonalData(doc, localValues); err != nil {
		return err
	}
	if err := stageWording(doc); err != nil {
		return err
	}
	return stageSchema(doc)
}

func isTyped(rec map[string]any) bool {
	_, hasType := rec["type"].(string)
	_, hasBlock := rec[Profile]
	return hasType && !hasBlock
}

func stageCanonicalization(doc any) error {
	rec, _ := doc.(map[string]any)
	var blk map[string]any
	if rec != nil && isTyped(rec) {
		blk = rec
	} else if rec != nil {
		blk, _ = rec[Profile].(map[string]any)
	}
	if blk == nil {
		return stageErr(StageCanonicalization, "no x-deal-v0 block")
	}
	if c := blk["canonicalization"]; c != Canonicalization {
		return stageErr(StageCanonicalization, `canonicalization must be exactly "jcs" (got %v); fail closed`, c)
	}
	if _, err := canonical.JCS(doc); err != nil {
		return stageErr(StageCanonicalization, "not JCS-serializable: %v", err)
	}
	return noFloats(doc, "$")
}

func noFloats(v any, path string) error {
	switch x := v.(type) {
	case json.Number:
		if strings.ContainsAny(string(x), ".eE") {
			return stageErr(StageCanonicalization, "floating-point value at %s; money is integer minor units", path)
		}
	case map[string]any:
		for _, k := range sortedKeys(x) {
			if err := noFloats(x[k], path+"."+k); err != nil {
				return err
			}
		}
	case []any:
		for i, e := range x {
			if err := noFloats(e, fmt.Sprintf("%s[%d]", path, i)); err != nil {
				return err
			}
		}
	}
	return nil
}

// recordString is one string a record carries, a value or a member name,
// and where.
type recordString struct{ path, s string }

func recordStrings(v any, path string, out *[]recordString) {
	switch x := v.(type) {
	case string:
		*out = append(*out, recordString{path, x})
	case map[string]any:
		for _, k := range sortedKeys(x) {
			*out = append(*out, recordString{path + ".<key>", k})
			recordStrings(x[k], path+"."+k, out)
		}
	case []any:
		for i, e := range x {
			recordStrings(e, fmt.Sprintf("%s[%d]", path, i), out)
		}
	}
}

func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

var (
	scanExempt = regexp.MustCompile(`^([0-9a-f]{16,}|(deal|sale)-[0-9a-f]+|\d{4}-\d{2}-\d{2}(T\d{2}:\d{2}:\d{2}Z)?|[a-z0-9-]+/[a-z0-9-]+/\d+\.\d+\.\d+)$`)
	scanPhone  = regexp.MustCompile(`\+?\(?\d[\d\s().-]{6,}\d`)
	scanEmail  = regexp.MustCompile(`[^\s@]+@[^\s@]+\.[^\s@]+`)
	scanWords  = regexp.MustCompile(`[^a-z]+`)
	// bannedWords label or grade a counterparty; a record never carries them.
	bannedWords = map[string]bool{
		"scam": true, "scams": true, "scammer": true, "scammers": true, "scammy": true, "fraud": true, "frauds": true,
		"fraudster": true, "fraudulent": true, "score": true, "scores": true, "scored": true, "scoring": true,
		"rating": true, "ratings": true, "reputation": true, "reputational": true, "blacklist": true,
		"blacklisted": true, "blocklist": true, "trustworthiness": true,
	}
)

// stagePersonalData refuses a raw phone number, email address or local-store
// value anywhere in the record, a member name included.
func stagePersonalData(doc any, localValues []string) error {
	var strs []recordString
	recordStrings(doc, "$", &strs)
	for _, rs := range strs {
		s := rs.s
		if scanExempt.MatchString(s) {
			continue
		}
		for _, m := range scanPhone.FindAllString(s, -1) {
			if len(nonDigit.ReplaceAllString(m, "")) >= 7 {
				return stageErr(StagePersonalData, "raw phone number at %s", rs.path)
			}
		}
		if scanEmail.MatchString(s) {
			return stageErr(StagePersonalData, "raw email address at %s", rs.path)
		}
		low := foldCase.String(s)
		for _, raw := range localValues {
			if raw != "" && strings.Contains(low, foldCase.String(raw)) {
				return stageErr(StagePersonalData, "raw local-store identifier at %s", rs.path)
			}
		}
	}
	return nil
}

func stageWording(doc any) error {
	var strs []recordString
	recordStrings(doc, "$", &strs)
	for _, rs := range strs {
		for _, tok := range scanWords.Split(strings.ToLower(rs.s), -1) {
			if bannedWords[tok] {
				return stageErr(StageWording, "prohibited word %q at %s", tok, rs.path)
			}
		}
	}
	return nil
}

func stageSchema(doc any) error {
	rec, _ := doc.(map[string]any)
	schema, err := profileSchema()
	if rec != nil && isTyped(rec) {
		t := rec["type"].(string)
		if _, ok := typedActionTypes[t]; !ok {
			return stageErr(StageSchema, "type: %q is not a typed action record", t)
		}
		schema, err = RecordSchema(t)
	}
	if err != nil {
		return err
	}
	if err := schema.Validate(doc); err != nil {
		return stageErr(StageSchema, "%s", firstSchemaError(err))
	}
	return nil
}

var schemaPrinter = message.NewPrinter(language.English)

// firstSchemaError is the first leaf of a validation error: where, and why.
func firstSchemaError(err error) string {
	v, ok := err.(*jsonschema.ValidationError)
	if !ok {
		return err.Error()
	}
	for len(v.Causes) > 0 {
		v = v.Causes[0]
	}
	return "/" + strings.Join(v.InstanceLocation, "/") + ": " + v.ErrorKind.LocalizedString(schemaPrinter)
}
