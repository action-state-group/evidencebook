// SPDX-License-Identifier: Apache-2.0

package ucp

import (
	"bytes"
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"github.com/action-state-group/evidencebook/adapter"
	"github.com/action-state-group/evidencebook/adapter/internal/ijson"
)

// The §4(e) properties of the Order Event Webhook, in declaration order.
const (
	PropOrderSchema       = "ucp.order.schema"
	PropWebhookComponents = "ucp.webhook.components"
	PropWebhookCoverage   = "ucp.webhook.coverage"
	PropWebhookDigest     = "ucp.webhook.content_digest"
	PropWebhookKey        = "ucp.webhook.key"
	PropWebhookSignature  = "ucp.webhook.signature"
	// CheckOrder is the object name of the order, and the check name of
	// the one native_check reported for an order body held alone (§4(d),
	// §5): NOT_PRESENT.
	CheckOrder = "ucp.order"
)

const (
	methodComponents    = "RFC 9421 §2 component set"
	methodCoverage      = "UCP Identity Resolution Algorithm step 5"
	methodDigest        = "RFC 9530 sha-256 over raw body"
	methodWebhookNative = "UCP Message Signatures §REST Request Verification (verify_rest_request): RFC 9421 over the request as received, RFC 9530 Content-Digest, key by keyid from the keys[] of the profile in UCP-Agent"
)

func methodWebhookSignature(alg string) string {
	if alg == "" {
		alg = "<alg>"
	}
	return "RFC 9421 " + alg + " over signature base"
}

var (
	requestLine = regexp.MustCompile(`^([A-Z]+) (\S+) HTTP/\d(?:\.\d)?$`)
	statusLine  = regexp.MustCompile(`^HTTP/\d(?:\.\d)? (\d{3})(?: .*)?$`)
)

// httpRequest is an HTTP/1.x message as received: a request (method,
// target) or a response (status), field lines, body.
type httpRequest struct {
	method, target string
	status         string              // set for a response
	fields         map[string][]string // lowercase name -> field line values, in order
	body           []byte
}

func (h *httpRequest) has(name string) bool { _, ok := h.fields[name]; return ok }

// value is the RFC 9421 §2.1 component value of a field: each field line's
// value with surrounding whitespace removed, joined by ", ".
func (h *httpRequest) value(name string) string {
	vs := h.fields[name]
	out := make([]string, len(vs))
	for i, v := range vs {
		out[i] = strings.Trim(v, " \t")
	}
	return strings.Join(out, ", ")
}

// looksLikeRequest reports whether raw starts with an HTTP/1.x request line.
func looksLikeRequest(raw []byte) bool {
	line, _, _ := bytes.Cut(raw, []byte("\n"))
	return requestLine.Match(bytes.TrimSuffix(line, []byte("\r")))
}

// looksLikeResponse reports whether raw starts with an HTTP/1.x status line.
func looksLikeResponse(raw []byte) bool {
	line, _, _ := bytes.Cut(raw, []byte("\n"))
	return statusLine.Match(bytes.TrimSuffix(line, []byte("\r")))
}

// parseRequest reads an HTTP/1.1 request message, or a response message
// when it starts with a status line. Lines end in CRLF or LF; the body is
// every octet after the empty line, unchanged.
func parseRequest(raw []byte) (*httpRequest, error) {
	h := &httpRequest{fields: map[string][]string{}}
	rest := raw
	first := true
	for {
		line, after, found := bytes.Cut(rest, []byte("\n"))
		if !found {
			return nil, errors.New("no empty line ends the header section")
		}
		rest = after
		l := string(bytes.TrimSuffix(line, []byte("\r")))
		if first {
			if m := statusLine.FindStringSubmatch(l); m != nil {
				h.status = m[1]
				first = false
				continue
			}
			m := requestLine.FindStringSubmatch(l)
			if m == nil {
				return nil, errors.New("no HTTP/1.x request or status line")
			}
			h.method, h.target = m[1], m[2]
			first = false
			continue
		}
		if l == "" {
			break
		}
		name, val, ok := strings.Cut(l, ":")
		if !ok || name == "" || strings.ContainsAny(name, " \t") {
			return nil, fmt.Errorf("malformed field line %q", l)
		}
		name = strings.ToLower(name)
		h.fields[name] = append(h.fields[name], val)
	}
	h.body = rest
	return h, nil
}

// pathQuery splits the request target (origin-form or absolute-form).
func (h *httpRequest) pathQuery() (path, query string, hasQuery bool, err error) {
	u, err := url.Parse(h.target)
	if err != nil {
		return "", "", false, err
	}
	path = u.EscapedPath()
	if path == "" {
		path = "/"
	}
	return path, u.RawQuery, u.ForceQuery || u.RawQuery != "", nil
}

// componentValue derives one covered component (RFC 9421 §2.1, §2.2). A
// component with parameters, or a derived component other than these, is
// not supported.
func (h *httpRequest) componentValue(c sfItem) (string, error) {
	name, _ := c.val.(string)
	if len(c.params) > 0 {
		return "", fmt.Errorf("component %q has parameters, which this verifier does not support", name)
	}
	if h.status != "" {
		// A response: @status, and fields (RFC 9421 §2.2.9).
		switch {
		case name == "@status":
			return h.status, nil
		case strings.HasPrefix(name, "@"):
			return "", fmt.Errorf("derived component %q is not supported on a response", name)
		case name != strings.ToLower(name) || !h.has(name):
			return "", errNotHeld
		}
		return h.value(name), nil
	}
	path, query, hasQuery, err := h.pathQuery()
	if err != nil {
		return "", err
	}
	switch name {
	case "@method":
		return h.method, nil
	case "@authority":
		if !h.has("host") {
			return "", errNotHeld
		}
		return strings.ToLower(h.value("host")), nil
	case "@path":
		return path, nil
	case "@query":
		if !hasQuery {
			return "?", nil
		}
		return "?" + query, nil
	case "@request-target":
		return h.target, nil
	}
	if strings.HasPrefix(name, "@") {
		return "", fmt.Errorf("derived component %q is not supported", name)
	}
	if name != strings.ToLower(name) || !h.has(name) {
		return "", errNotHeld
	}
	return h.value(name), nil
}

var errNotHeld = errors.New("not held")

// checkOrderBody reports §4(e) for an order body held without its request:
// the webhook rows are NOT_PRESENT and native_check is NOT_PRESENT (§4(d),
// §5).
func checkOrderBody(v any, policy adapter.TrustPolicy) []adapter.PropertyResult {
	r := newRows()
	r.orderSchema(v, nil)
	for _, p := range []string{PropWebhookComponents, PropWebhookCoverage, PropWebhookDigest, PropWebhookKey, PropWebhookSignature} {
		m := methodComponents
		switch p {
		case PropWebhookCoverage:
			m = methodCoverage
		case PropWebhookDigest:
			m = methodDigest
		case PropWebhookKey:
			m = methodKeyLive
		case PropWebhookSignature:
			m = methodWebhookSignature("")
		}
		r.add(p, adapter.NotPresent, m, "only the body is held, not the signed request")
	}
	r.add(CheckOrder, adapter.NotPresent, methodNoSignature, "the order is not signed as an object; holding it proves possession of its octets only")
	r.businessTrust(policy, policy.Pin(PinKeysOrigin))
	return r.out
}

func (r *rows) orderSchema(v any, parseErr error) {
	switch {
	case parseErr != nil:
		r.add(PropOrderSchema, adapter.Fail, methodSchema, "not I-JSON: "+parseErr.Error())
	default:
		ok, why, err := validate(true, v)
		switch {
		case err != nil:
			r.add(PropOrderSchema, adapter.NotChecked, methodSchema, "schemas unavailable: "+err.Error())
		case ok:
			r.add(PropOrderSchema, adapter.Pass, methodSchema, "")
		default:
			r.add(PropOrderSchema, adapter.Fail, methodSchema, why)
		}
	}
}

// checkWebhook reports §4(e) for a webhook request held whole.
func checkWebhook(raw []byte, policy adapter.TrustPolicy) []adapter.PropertyResult {
	h, err := parseRequest(raw)
	if err != nil {
		r := newRows()
		r.add(PropOrderSchema, adapter.NotChecked, methodSchema, "the request could not be read: "+err.Error())
		r.add(PropWebhookComponents, adapter.Fail, methodComponents, "the request could not be read: "+err.Error())
		for _, p := range []string{PropWebhookCoverage, PropWebhookDigest, PropWebhookKey, PropWebhookSignature} {
			r.add(p, adapter.NotChecked, "", "the request could not be read")
		}
		r.businessTrust(policy, "")
		return r.out
	}
	r := newRows()
	body, perr := ijson.Parse(h.body)
	r.orderSchema(body, perr)

	origin, agentErr := ucpAgentOrigin(h)
	labels, inputs, sigs, sfErr := signatureHeaders(h)
	var best []adapter.PropertyResult
	if sfErr != nil {
		best = []adapter.PropertyResult{
			{Property: PropWebhookComponents, Status: adapter.NotPresent, Method: methodComponents, Detail: sfErr.Error()},
			{Property: PropWebhookCoverage, Status: adapter.NotPresent, Method: methodCoverage, Detail: sfErr.Error()},
			digestRow(h),
			{Property: PropWebhookKey, Status: adapter.NotPresent, Method: methodKeyLive, Detail: sfErr.Error()},
			{Property: PropWebhookSignature, Status: adapter.NotPresent, Method: methodWebhookSignature(""), Detail: sfErr.Error()},
		}
		if strings.HasPrefix(sfErr.Error(), "malformed") {
			for i := range best {
				if best[i].Property != PropWebhookDigest {
					best[i].Status = adapter.Fail
				}
			}
		}
	} else {
		// RFC 9421 §4.3 and UCP: try each signature; the request is
		// authenticated when one verifies. Report the first that passes
		// every row, else the first.
		for _, label := range labels {
			rs := checkLabel(h, label, inputs[label], sigs, origin, agentErr, policy)
			if best == nil {
				best = rs
			}
			if !slices.ContainsFunc(rs, func(p adapter.PropertyResult) bool { return p.Status != adapter.Pass }) {
				best = rs
				break
			}
		}
	}
	r.out = append(r.out, best...)
	r.businessTrust(policy, origin)
	return r.out
}

// signatureHeaders parses Signature-Input and Signature.
func signatureHeaders(h *httpRequest) ([]string, map[string]sfMember, map[string]sfMember, error) {
	if !h.has("signature-input") || !h.has("signature") {
		return nil, nil, nil, errors.New("the request carries no Signature-Input and Signature")
	}
	in, err := parseSFDictionary(h.value("signature-input"))
	if err != nil {
		return nil, nil, nil, fmt.Errorf("malformed Signature-Input: %v", err)
	}
	sg, err := parseSFDictionary(h.value("signature"))
	if err != nil {
		return nil, nil, nil, fmt.Errorf("malformed Signature: %v", err)
	}
	inputs, sigs := map[string]sfMember{}, map[string]sfMember{}
	var labels []string
	for _, m := range in {
		labels = append(labels, m.key)
		inputs[m.key] = m
	}
	for _, m := range sg {
		sigs[m.key] = m
	}
	if len(labels) == 0 {
		return nil, nil, nil, errors.New("malformed Signature-Input: no signature")
	}
	return labels, inputs, sigs, nil
}

// ucpAgentOrigin parses UCP-Agent (UCP Message Signatures, UCP-Agent parsing
// rules) and returns the origin of its profile URL.
func ucpAgentOrigin(h *httpRequest) (string, error) {
	if !h.has("ucp-agent") {
		return "", errNotHeld
	}
	d, err := parseSFDictionary(h.value("ucp-agent"))
	if err != nil {
		return "", fmt.Errorf("UCP-Agent is not an RFC 8941 Dictionary: %v", err)
	}
	for _, m := range d {
		if m.key != "profile" || m.isList {
			continue
		}
		s, ok := m.item.val.(string)
		if !ok {
			return "", errors.New("UCP-Agent profile is not a string")
		}
		u, err := url.Parse(s)
		if err != nil || u.Scheme != "https" || u.Host == "" {
			return "", errors.New("UCP-Agent profile is not an HTTPS URL")
		}
		if u.Path != "/.well-known/ucp" {
			return "", errors.New("UCP-Agent profile of a business must be /.well-known/ucp")
		}
		return "https://" + strings.ToLower(u.Host), nil
	}
	return "", errors.New("UCP-Agent has no profile")
}

func digestRow(h *httpRequest) adapter.PropertyResult {
	row := adapter.PropertyResult{Property: PropWebhookDigest, Method: methodDigest}
	if !h.has("content-digest") {
		if len(h.body) == 0 {
			row.Status, row.Detail = adapter.Pass, "no body"
		} else {
			row.Status, row.Detail = adapter.NotPresent, "the request has a body and no Content-Digest"
		}
		return row
	}
	d, err := parseSFDictionary(h.value("content-digest"))
	if err != nil {
		row.Status, row.Detail = adapter.Fail, "Content-Digest is not an RFC 8941 Dictionary: "+err.Error()
		return row
	}
	for _, m := range d {
		if m.key != "sha-256" || m.isList {
			continue
		}
		got, ok := m.item.val.([]byte)
		if !ok {
			break
		}
		want := sha256.Sum256(h.body)
		if bytes.Equal(got, want[:]) {
			row.Status = adapter.Pass
		} else {
			row.Status, row.Detail = adapter.Fail, "digest_mismatch: sha-256 of the raw body differs from Content-Digest"
		}
		return row
	}
	row.Status, row.Detail = adapter.Fail, "Content-Digest has no sha-256 byte sequence, which UCP requires"
	return row
}

// checkLabel reports rows components..signature for one signature label.
func checkLabel(h *httpRequest, label string, in sfMember, sigs map[string]sfMember, origin string, agentErr error, policy adapter.TrustPolicy) []adapter.PropertyResult {
	var out []adapter.PropertyResult
	add := func(p string, st adapter.Status, m, d string) {
		out = append(out, adapter.PropertyResult{Property: p, Status: st, Method: m, Detail: d})
	}
	at := "signature " + label + ": "
	if !in.isList {
		add(PropWebhookComponents, adapter.Fail, methodComponents, at+"Signature-Input member is not an inner list")
		add(PropWebhookCoverage, adapter.NotChecked, methodCoverage, at+"no covered components")
		out = append(out, digestRow(h))
		add(PropWebhookKey, adapter.NotChecked, methodKeyLive, at+"no keyid")
		add(PropWebhookSignature, adapter.NotChecked, methodWebhookSignature(""), at+"no covered components")
		return out
	}

	// ucp.webhook.components: every covered component is held.
	var names, missing []string
	values := map[string]string{}
	var unsupported error
	dup := false
	for _, c := range in.inner {
		name, ok := c.val.(string)
		if !ok {
			unsupported = errors.New("a component identifier is not a string")
			continue
		}
		if slices.Contains(names, name) {
			dup = true
		}
		names = append(names, name)
		v, err := h.componentValue(c)
		switch {
		case errors.Is(err, errNotHeld):
			missing = append(missing, name)
		case err != nil:
			unsupported = err
		default:
			values[name] = v
		}
	}
	switch {
	case len(missing) > 0:
		add(PropWebhookComponents, adapter.NotPresent, methodComponents, at+"covered but not held: "+strings.Join(missing, ", "))
	default:
		add(PropWebhookComponents, adapter.Pass, methodComponents, at+strings.Join(names, " "))
	}

	// ucp.webhook.coverage: UCP's required set (IRA step 5).
	_, _, hasQuery, _ := h.pathQuery()
	required := []string{"@method", "@authority", "@path"}
	if hasQuery {
		required = append(required, "@query")
	}
	if len(h.body) > 0 {
		required = append(required, "content-digest", "content-type")
	}
	for _, hn := range []string{"idempotency-key", "ucp-agent", "signature-agent"} {
		if h.has(hn) {
			required = append(required, hn)
		}
	}
	var uncovered []string
	for _, c := range required {
		if !slices.Contains(names, c) {
			uncovered = append(uncovered, c)
		}
	}
	if len(uncovered) > 0 {
		add(PropWebhookCoverage, adapter.Fail, methodCoverage, at+"coverage_insufficient: not covered: "+strings.Join(uncovered, ", "))
	} else {
		add(PropWebhookCoverage, adapter.Pass, methodCoverage, at+"covers "+strings.Join(required, ", "))
	}

	// ucp.webhook.content_digest
	out = append(out, digestRow(h))

	// ucp.webhook.key
	kidV, _ := in.param("keyid")
	kid, _ := kidV.(string)
	var pub crypto.PublicKey
	var keyRow adapter.PropertyResult
	switch {
	case kid == "":
		keyRow = adapter.PropertyResult{Property: PropWebhookKey, Status: adapter.Fail, Method: methodKeyLive, Detail: at + "Signature-Input has no keyid"}
	case errors.Is(agentErr, errNotHeld):
		keyRow = adapter.PropertyResult{Property: PropWebhookKey, Status: adapter.NotPresent, Method: methodKeyLive, Detail: at + "no UCP-Agent names the signer's profile"}
	case agentErr != nil:
		keyRow = adapter.PropertyResult{Property: PropWebhookKey, Status: adapter.Fail, Method: methodKeyLive, Detail: at + agentErr.Error()}
	default:
		pub, keyRow = resolveKey(policy, kid, origin, PropWebhookKey)
		keyRow.Detail = at + keyRow.Detail
	}
	out = append(out, keyRow)

	// ucp.webhook.signature
	tagV, _ := in.param("tag")
	tag, _ := tagV.(string)
	algName, hash := "", crypto.Hash(0)
	switch k := pub.(type) {
	case *ecdsa.PublicKey:
		switch k.Curve.Params().Name {
		case "P-256":
			algName, hash = "ecdsa-p256-sha256", crypto.SHA256
		case "P-384":
			algName, hash = "ecdsa-p384-sha384", crypto.SHA384
		}
	case ed25519.PublicKey:
		algName = "ed25519"
	}
	sm := methodWebhookSignature(algName)
	sigMember, haveSig := sigs[label]
	var sigBytes []byte
	if haveSig && !sigMember.isList {
		sigBytes, _ = sigMember.item.val.([]byte)
	}
	switch {
	case tag != "" && tag != "web-bot-auth":
		add(PropWebhookSignature, adapter.NotChecked, sm, at+"tag "+quote(tag)+" is not one UCP defines or accepts")
	case unsupported != nil:
		add(PropWebhookSignature, adapter.NotChecked, sm, at+unsupported.Error())
	case !haveSig || sigBytes == nil:
		add(PropWebhookSignature, adapter.NotPresent, sm, at+"no Signature byte sequence for this label")
	case len(missing) > 0:
		add(PropWebhookSignature, adapter.NotChecked, sm, at+"a covered component is not held")
	case dup:
		add(PropWebhookSignature, adapter.Fail, sm, at+"a component is covered twice (RFC 9421 §2.5)")
	case keyRow.Status != adapter.Pass:
		add(PropWebhookSignature, adapter.NotChecked, sm, at+"no usable key")
	case algName == "":
		add(PropWebhookSignature, adapter.NotChecked, sm, at+"the key's curve is not one this verifier implements for RFC 9421")
	default:
		if a, ok := in.param("alg"); ok && a != algName {
			add(PropWebhookSignature, adapter.Fail, sm, fmt.Sprintf("%salg parameter %v does not match the key (%s)", at, a, algName))
			break
		}
		if tag == "web-bot-auth" {
			jk, _, _ := mustKeySet(policy).lookup(kid)
			if tp, err := jk.thumbprint(); err != nil || tp != kid {
				add(PropWebhookSignature, adapter.Fail, sm, at+"signature_invalid: web-bot-auth keyid is not the key's RFC 7638 thumbprint")
				break
			}
		}
		base, err := signatureBase(in, values)
		if err != nil {
			add(PropWebhookSignature, adapter.Fail, sm, at+err.Error())
			break
		}
		var ok bool
		switch k := pub.(type) {
		case *ecdsa.PublicKey:
			ok = verifyECDSA(k, hash, []byte(base), sigBytes)
		case ed25519.PublicKey:
			ok = ed25519.Verify(k, []byte(base), sigBytes)
		}
		if ok {
			add(PropWebhookSignature, adapter.Pass, sm, at+"kid "+quote(kid))
		} else {
			add(PropWebhookSignature, adapter.Fail, sm, at+"signature_invalid: the signature does not verify over the rebuilt signature base")
		}
	}
	return out
}

func mustKeySet(policy adapter.TrustPolicy) keySet {
	ks, _ := parseKeySet(policy.KeySets[KeySetName])
	return ks
}

// signatureBase builds the RFC 9421 §2.5 signature base.
func signatureBase(in sfMember, values map[string]string) (string, error) {
	var b strings.Builder
	for _, c := range in.inner {
		name := c.val.(string)
		id, err := serializeBare(name)
		if err != nil {
			return "", err
		}
		v := values[name]
		if strings.ContainsAny(v, "\r\n") {
			return "", fmt.Errorf("component %s has a line break", name)
		}
		b.WriteString(id + ": " + v + "\n")
	}
	params, err := serializeInnerList(in.inner, in.params)
	if err != nil {
		return "", err
	}
	b.WriteString(`"@signature-params": ` + params)
	return b.String(), nil
}
