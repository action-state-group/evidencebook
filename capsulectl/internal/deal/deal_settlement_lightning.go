package deal

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	aacsettlement "github.com/action-state-group/agent-action-capsule/go/settlement"
	"github.com/action-state-group/capsule-cli/settlement"
	"github.com/action-state-group/evidencebook/dealprofile"
)

// A Lightning payment the payee received, read from the payee's own wallet:
// the invoice it issued (as `mesh-llm wallet fund-wallet` prints it) and the
// wallet's inbound transaction for that invoice (an entry of `mesh-llm wallet
// get-transactions`). The leg names the payment by its hash
// (payment_ref ln.payment_hash). Neither file is wrapped. Steven ruled what
// is sealed (Buzz 79b46dfb item 5): the preimage, the proof the payment
// settled, once its SHA-256 is the payment hash, on the step's record
// (body_evidence preimage); never the invoice's BOLT11 string.

// lightningBTC is bitcoin mainnet as a CAIP-19 asset, counted in millisatoshi
// (scale 11). A buyer's terms leg names the same asset
// (`deal terms-leg --asset lightningBTC --asset-scale 11`), so the verifier
// compares like with like.
const (
	lightningBTC      = "bip122:000000000019d6689c085ae165831e93/slip44:0"
	lightningBTCScale = 11
)

var (
	paymentHashPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)
	nodeIDPattern      = regexp.MustCompile(`^0[23][0-9a-f]{64}$`)
)

// lightningInvoice is the part of the payee's issued invoice the leg reads.
type lightningInvoice struct {
	PaymentHash string  `json:"payment_hash"`
	Payee       string  `json:"payee"`
	AmountMsat  *uint64 `json:"amount_msat"`
}

// lightningTransaction is the part of the wallet's transaction the leg reads.
type lightningTransaction struct {
	PaymentHash *string `json:"payment_hash"`
	Inbound     *bool   `json:"inbound"`
	AmountMsat  *uint64 `json:"amount_msat"`
	FeeMsat     *uint64 `json:"fee_msat"`
	Status      string  `json:"status"`
	SettledAtMs *uint64 `json:"settled_at_ms"`
	Preimage    *string `json:"preimage"`
}

func decodeObject(name string, data []byte, into any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(into); err != nil {
		return fmt.Errorf("%s is not a JSON object: %w", name, err)
	}
	if decoder.More() {
		return errors.New(name + " has trailing data")
	}
	return nil
}

// payeeObservedLightning builds the payee_observed leg for one received
// Lightning payment. received is what the wallet credited and receive_fee
// what it deducted, so received + receive_fee is the invoice amount. The
// payee's node id is an account identifier: it binds the deal's per-deal
// payee fingerprint and never enters the leg. The preimage it returns, when
// the wallet reported one, has been checked against the payment hash.
func payeeObservedLightning(invoiceJSON, transactionJSON []byte, termsRef, observedAt string) (settlement.Built, string, error) {
	var inv lightningInvoice
	if err := decodeObject("the invoice", invoiceJSON, &inv); err != nil {
		return settlement.Built{}, "", err
	}
	var tx lightningTransaction
	if err := decodeObject("the transaction", transactionJSON, &tx); err != nil {
		return settlement.Built{}, "", err
	}
	switch {
	case !paymentHashPattern.MatchString(inv.PaymentHash):
		return settlement.Built{}, "", errors.New("the invoice needs payment_hash, 64 lowercase hex characters")
	case !nodeIDPattern.MatchString(inv.Payee):
		return settlement.Built{}, "", errors.New("the invoice needs payee, the node id it pays (66 lowercase hex characters)")
	case inv.AmountMsat == nil || *inv.AmountMsat == 0:
		return settlement.Built{}, "", errors.New("the invoice needs a fixed amount_msat: an amount-less invoice does not say what the price was")
	case tx.PaymentHash == nil || *tx.PaymentHash != inv.PaymentHash:
		return settlement.Built{}, "", errors.New("the transaction is not this invoice's payment: payment_hash differs")
	case tx.Inbound == nil || !*tx.Inbound:
		return settlement.Built{}, "", errors.New("the transaction is not inbound: payment_received is a payment your wallet received")
	case tx.AmountMsat == nil || tx.FeeMsat == nil:
		return settlement.Built{}, "", errors.New("the transaction needs amount_msat and fee_msat as the wallet reported them")
	}
	var status string
	switch tx.Status {
	case "succeeded":
		status = "settled"
	case "failed":
		status = "failed"
	case "pending":
		return settlement.Built{}, "", errors.New("the payment is still pending in your wallet: note it once the wallet reports it succeeded")
	default:
		return settlement.Built{}, "", errors.New("the transaction's status is not one a wallet reports (pending, succeeded, failed)")
	}
	var preimage string
	if tx.Preimage != nil {
		preimage = *tx.Preimage
		if status != "settled" {
			return settlement.Built{}, "", errors.New("the transaction carries a preimage but did not succeed")
		}
		if !dealprofile.PreimageMatches(preimage, inv.PaymentHash) {
			return settlement.Built{}, "", errors.New("the transaction's preimage is not this payment's: its SHA-256 is not the payment hash")
		}
	}
	if status == "settled" {
		if *tx.AmountMsat+*tx.FeeMsat < *tx.AmountMsat || *tx.AmountMsat+*tx.FeeMsat != *inv.AmountMsat {
			return settlement.Built{}, "", fmt.Errorf("the wallet credited %d msat and deducted %d msat, which is not the invoice's %d msat", *tx.AmountMsat, *tx.FeeMsat, *inv.AmountMsat)
		}
		if tx.SettledAtMs != nil && time.UnixMilli(int64(*tx.SettledAtMs)).UTC().Truncate(time.Second).After(mustTime(observedAt)) {
			return settlement.Built{}, "", errors.New("--observed-at is before the wallet says the payment settled")
		}
	}
	amountOf := func(msat uint64) map[string]any {
		return map[string]any{"value": fmt.Sprint(msat), "assetCode": lightningBTC, "assetScale": json.Number(fmt.Sprint(lightningBTCScale))}
	}
	b := settlement.Built{Objects: map[string][]byte{}, AccountIDs: []string{inv.Payee}}
	b.Member = map[string]any{
		"version": settlement.Version, "leg": settlement.LegPayeeObserved, "sealer_role": settlement.RolePayee, "terms_ref": termsRef,
		"received": amountOf(*tx.AmountMsat), "receive_fee": amountOf(*tx.FeeMsat),
		"payment_ref": map[string]any{"type": "ln.payment_hash", "value": inv.PaymentHash},
		"status":      status, "observed_at": observedAt,
	}
	if failures := aacsettlement.StructureFailures(b.Member); len(failures) > 0 {
		return settlement.Built{}, "", errors.New("the leg would not verify: " + strings.Join(failures, ", "))
	}
	return b, preimage, settlement.NeverEnters(b.Member, b.AccountIDs)
}

func mustTime(rfc3339 string) time.Time {
	t, _ := time.Parse(time.RFC3339, rfc3339)
	return t
}
