// SPDX-License-Identifier: Apache-2.0

package acp

import (
	"bytes"
	_ "embed"
	"fmt"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
)

// ACP's published JSON Schema bundles, vendored unchanged (schema/NOTICE).
var (
	//go:embed schema/schema.agentic_checkout.json
	schemaCheckout []byte
	//go:embed schema/schema.delegate_payment.json
	schemaDelegatePayment []byte
)

// The SHA-256 of each vendored file at ACP commit 7fdd78df, as
// agent-action-capsule spec/adapters/acp-v0.md §1 records them.
const (
	SchemaCommit                = "7fdd78df677a94dce04c770644b0fbbb1401272b"
	SchemaCheckoutPath          = "spec/2026-04-17/json-schema/schema.agentic_checkout.json"
	SchemaCheckoutSHA256        = "d0e4290617d66bf05d002b8ace388732be2b3eb9a92a1003db7a2daa1e0436f2"
	SchemaDelegatePaymentPath   = "spec/2026-04-17/json-schema/schema.delegate_payment.json"
	SchemaDelegatePaymentSHA256 = "307739ae400e7368eaa25c6024d30751348a4af8a516d071565fcd8aa328cf4d"
)

// The bundles' own $id values. Nothing is fetched from them.
const (
	idCheckout        = "https://example.com/schemas/agentic-checkout/bundle.schema.json"
	idDelegatePayment = "https://example.com/schemas/delegate-payment/bundle.schema.json"
	idOrderEvent      = "urn:evidencebook:acp:order-event"
)

// schemaOrderEvent is the order event body of OAS-WH (openapi.agentic_checkout_webhook.yaml,
// components WebhookEvent and EventDataOrder), which ACP publishes only in
// OpenAPI. It is evidencebook's transcription, not an ACP file.
//
// EventDataOrder is OAS-CHK's Order with type, checkout_session_id,
// permalink_url and status also required. At 7fdd78df, OAS-CHK's Order and
// the definitions it reaches are JS-CHK's except for three optional members
// that JS-CHK adds and OAS-CHK does not allow: Order.client_reference_id,
// OrderConfirmation.order_notes and Address.company (reached from an order
// at fulfillments[].destination). The transcription is JS-CHK's Order with
// those three excluded, so it accepts what OAS-WH's EventDataOrder accepts.
var schemaOrderEvent = []byte(`{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "` + idOrderEvent + `",
  "type": "object",
  "additionalProperties": false,
  "properties": {
    "type": {"type": "string"},
    "data": {
      "allOf": [{"$ref": "` + idCheckout + `#/$defs/Order"}],
      "required": ["type", "checkout_session_id", "permalink_url", "status"],
      "not": {"required": ["client_reference_id"]},
      "properties": {
        "confirmation": {"not": {"required": ["order_notes"]}},
        "fulfillments": {"items": {"properties": {"destination": {"not": {"required": ["company"]}}}}}
      }
    }
  },
  "required": ["type", "data"]
}`)

// noLoader refuses every external reference: the three schemas are added as
// resources, and validation never reaches the network or the file system.
type noLoader struct{}

func (noLoader) Load(url string) (any, error) {
	return nil, fmt.Errorf("external schema %s is not loaded", url)
}

var (
	compileOnce sync.Once
	compiled    map[string]*jsonschema.Schema
	compileErr  error
)

// Schema locations, each object kind's schema. Every one is a JS-CHK or
// JS-DP definition validated as a whole object, except the order event,
// which is the OAS-WH transcription above. The adapter never validates a
// checkout against OAS-CHK, so OAS-CHK's CheckoutSessionWithOrder defect
// (acp-v0 §6, open point A-4) does not arise: JS-CHK's CheckoutSessionBase
// lists order, and a completed session validates against JS-CHK whole.
var schemaRef = map[kind]string{
	kindCheckoutSession:          idCheckout + "#/$defs/CheckoutSession",
	kindCheckoutSessionWithOrder: idCheckout + "#/$defs/CheckoutSessionWithOrder",
	kindDelegatePayment:          idDelegatePayment + "#/$defs/DelegatePaymentResponse",
	kindOrderEvent:               idOrderEvent,
}

func schemas() (map[string]*jsonschema.Schema, error) {
	compileOnce.Do(func() {
		c := jsonschema.NewCompiler()
		c.UseLoader(noLoader{})
		c.DefaultDraft(jsonschema.Draft2020)
		for id, data := range map[string][]byte{
			idCheckout:        schemaCheckout,
			idDelegatePayment: schemaDelegatePayment,
			idOrderEvent:      schemaOrderEvent,
		} {
			doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
			if err != nil {
				compileErr = fmt.Errorf("schema %s: %w", id, err)
				return
			}
			if err := c.AddResource(id, doc); err != nil {
				compileErr = fmt.Errorf("schema %s: %w", id, err)
				return
			}
		}
		out := map[string]*jsonschema.Schema{}
		for _, ref := range schemaRef {
			s, err := c.Compile(ref)
			if err != nil {
				compileErr = fmt.Errorf("schema %s: %w", ref, err)
				return
			}
			out[ref] = s
		}
		compiled = out
	})
	return compiled, compileErr
}

// validate checks v, as decoded by decodeStrict, against the schema for k.
func validate(k kind, v any) error {
	s, err := schemas()
	if err != nil {
		return err
	}
	if err := s[schemaRef[k]].Validate(v); err != nil {
		return fmt.Errorf("%s", flatten(err))
	}
	return nil
}

// flatten turns a validation error into one line naming each failing
// location, so it fits in a detail.
func flatten(err error) string {
	ve, ok := err.(*jsonschema.ValidationError)
	if !ok {
		return err.Error()
	}
	p := message.NewPrinter(language.English)
	var leaves []string
	var walk func(e *jsonschema.ValidationError)
	walk = func(e *jsonschema.ValidationError) {
		if len(e.Causes) == 0 {
			loc := "/" + strings.Join(e.InstanceLocation, "/")
			leaves = append(leaves, fmt.Sprintf("at %s: %s", loc, e.ErrorKind.LocalizedString(p)))
			return
		}
		for _, c := range e.Causes {
			walk(c)
		}
	}
	walk(ve)
	const max = 5
	if len(leaves) > max {
		leaves = append(leaves[:max], fmt.Sprintf("and %d more", len(leaves)-max))
	}
	return strings.Join(leaves, "; ")
}
