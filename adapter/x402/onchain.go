// SPDX-License-Identifier: Apache-2.0

package x402

import (
	"encoding/json"
	"fmt"
	"math/big"
	"strings"

	"github.com/action-state-group/evidencebook/adapter"
	"github.com/action-state-group/evidencebook/adapter/internal/ijson"
)

// RecordedLookup is the TrustPolicy.Recorded key of the relying party's
// recorded chain observation (declaration §6.4): a Lookup.
const RecordedLookup = "x402.lookup"

// Lookup is a recorded chain observation in the shapes a chain node returns
// (the transaction receipt and block of the JSON-RPC eth_getTransactionReceipt
// and eth_getBlockByNumber results), as in the agent-action-capsule x402
// vectors' lookup.json. It is the relying party's own observation; the
// adapter never makes the call.
type Lookup struct {
	Provenance         string      `json:"provenance,omitempty"`
	Network            string      `json:"network"`
	Source             string      `json:"source"`
	TransactionReceipt *RPCReceipt `json:"transaction_receipt"`
	Block              *RPCBlock   `json:"block"`
	// CodeAtPayer is the code at authorization.from ("0x" for an externally
	// owned account), when recorded.
	CodeAtPayer *string `json:"code_at_payer,omitempty"`
	// Final is false when the source did not consider the transaction
	// final; absent means the relying party recorded it as final.
	Final *bool `json:"final,omitempty"`
}

// RPCReceipt is the part of a transaction receipt the adapter reads.
type RPCReceipt struct {
	TransactionHash string   `json:"transactionHash"`
	BlockNumber     string   `json:"blockNumber"`
	Status          string   `json:"status"`
	From            string   `json:"from,omitempty"`
	To              string   `json:"to,omitempty"`
	Logs            []RPCLog `json:"logs"`
}

// RPCLog is one log entry of a receipt.
type RPCLog struct {
	Address string   `json:"address"`
	Topics  []string `json:"topics"`
	Data    string   `json:"data"`
}

// RPCBlock is the part of a block the adapter reads.
type RPCBlock struct {
	Number    string `json:"number"`
	Timestamp string `json:"timestamp"`
}

var (
	topicTransfer          = keccak256([]byte("Transfer(address,address,uint256)"))
	topicAuthorizationUsed = keccak256([]byte("AuthorizationUsed(address,bytes32)"))
)

func recordedLookup(policy adapter.TrustPolicy) (*Lookup, error) {
	raw, ok := policy.Recorded[RecordedLookup]
	if !ok {
		return nil, nil
	}
	if _, err := ijson.Parse(raw); err != nil {
		return nil, fmt.Errorf("recorded %s: %v", RecordedLookup, err)
	}
	var l Lookup
	if err := json.Unmarshal(raw, &l); err != nil {
		return nil, fmt.Errorf("recorded %s: %v", RecordedLookup, err)
	}
	return &l, nil
}

func lookupSource(policy adapter.TrustPolicy) string {
	if l, err := recordedLookup(policy); err == nil && l != nil && l.Source != "" {
		return "recorded lookup: " + l.Source
	}
	return "none (offline)"
}

// recordedNoCode reports whether the recorded lookup shows no code at the
// payer address.
func recordedNoCode(policy adapter.TrustPolicy) (bool, string) {
	l, err := recordedLookup(policy)
	if err != nil || l == nil || l.CodeAtPayer == nil {
		return false, ""
	}
	return *l.CodeAtPayer == "0x", l.Source
}

// lookupFor returns the recorded lookup when it is of the response's
// transaction on the response's network, and why not otherwise.
func lookupFor(resp *response, policy adapter.TrustPolicy) (*Lookup, string) {
	l, err := recordedLookup(policy)
	switch {
	case err != nil:
		return nil, err.Error()
	case l == nil:
		return nil, "offline: no recorded lookup"
	case l.Network != resp.network:
		return nil, fmt.Sprintf("offline: the recorded lookup is on %s, the response names %s", l.Network, resp.network)
	case l.TransactionReceipt != nil && !strings.EqualFold(l.TransactionReceipt.TransactionHash, resp.tx):
		return nil, "offline: the recorded lookup is of another transaction"
	}
	return l, ""
}

func hexQuantity(s string) (*big.Int, error) {
	h, ok := strings.CutPrefix(s, "0x")
	if !ok || h == "" {
		return nil, fmt.Errorf("%q is not a 0x hex quantity", s)
	}
	n, ok := new(big.Int).SetString(h, 16)
	if !ok {
		return nil, fmt.Errorf("%q is not a 0x hex quantity", s)
	}
	return n, nil
}

func topicWord(t string) ([32]byte, error) { return parseBytes32(strings.ToLower(t)) }

func topicAddress(t string) (address, error) {
	w, err := topicWord(t)
	var a address
	if err != nil {
		return a, err
	}
	copy(a[:], w[12:])
	return a, nil
}

// checkSettlementOnchain applies the declaration's §6.4 items 1 to 3 to the
// recorded lookup. Offline it is NOT_CHECKED.
func checkSettlementOnchain(o *out, resp *response, pay *payment, policy adapter.TrustPolicy) {
	src := lookupSource(policy)
	set := func(st adapter.Status, d string) {
		o.nativeX(RowSettlementOnchain, st, MethodSettleOnchain, d+" (source: "+src+")", nil)
	}
	if resp.tx == "" {
		d := "transaction is empty"
		if success, _ := resp.root.Bool("success"); !success {
			reason, _ := resp.root.Str("errorReason")
			d += fmt.Sprintf("; the response states success false, errorReason %q", reason)
		}
		set(adapter.NotPresent, d)
		return
	}
	l, why := lookupFor(resp, policy)
	if l == nil {
		set(adapter.NotChecked, why)
		return
	}
	rc := l.TransactionReceipt
	if rc == nil {
		set(adapter.Fail, "the transaction is absent on "+l.Network)
		return
	}
	switch rc.Status {
	case "0x1":
	case "0x0":
		set(adapter.Fail, "the transaction failed on "+l.Network)
		return
	default:
		set(adapter.Inconclusive, fmt.Sprintf("receipt status %q", rc.Status))
		return
	}
	if l.Final != nil && !*l.Final {
		set(adapter.Inconclusive, "the source did not consider the transaction final")
		return
	}
	if pay == nil || !pay.inScope {
		set(adapter.Inconclusive, "no in-scope payment payload held: the events cannot be matched to an authorization")
		return
	}
	auth, missing, aerr := pay.authorization()
	assetStr, _ := pay.accepted.Str("asset")
	asset, perr := parseAddress(assetStr)
	if missing != "" || aerr != nil || perr != nil {
		set(adapter.Inconclusive, "the authorization or accepted.asset is not readable: the events cannot be matched")
		return
	}
	var transfer, used bool
	for _, lg := range rc.Logs {
		emitter, err := parseAddress(lg.Address)
		if err != nil || emitter != asset || len(lg.Topics) != 3 {
			continue
		}
		t0, err := topicWord(lg.Topics[0])
		if err != nil {
			continue
		}
		switch string(t0[:]) {
		case string(topicTransfer):
			f, e1 := topicAddress(lg.Topics[1])
			t, e2 := topicAddress(lg.Topics[2])
			v, e3 := hexQuantity(lg.Data)
			if e1 == nil && e2 == nil && e3 == nil && f == auth.From && t == auth.To && v.Cmp(auth.Value) == 0 {
				transfer = true
			}
		case string(topicAuthorizationUsed):
			a, e1 := topicAddress(lg.Topics[1])
			n, e2 := topicWord(lg.Topics[2])
			if e1 == nil && e2 == nil && a == auth.From && n == auth.Nonce {
				used = true
			}
		}
	}
	var fails []string
	if !transfer {
		fails = append(fails, "no Transfer(from, to, value) by accepted.asset matching the authorization")
	}
	if !used {
		fails = append(fails, "no AuthorizationUsed(authorizer, nonce) by accepted.asset matching the authorization")
	}
	if len(fails) > 0 {
		set(adapter.Fail, strings.Join(fails, "; "))
		return
	}
	set(adapter.Pass, "status success; Transfer and AuthorizationUsed match the authorization")
}

// checkAuthorizationWindow reports authorization_window: EIP-3009's strict
// validAfter < t < validBefore, t the settling block's timestamp from the
// recorded lookup (ruling Q3, declaration §6.1). Never evaluation time.
func checkAuthorizationWindow(o *out, pay *payment, responseHeld bool, resp *response, respErr error, policy adapter.TrustPolicy) {
	set := func(st adapter.Status, d string) { o.native(RowAuthorizationWindow, st, MethodAuthWindow, d) }
	switch {
	case pay == nil && o.lastStatus(RowPayloadForm) == adapter.NotPresent:
		set(adapter.NotPresent, "no payment payload held")
		return
	case pay == nil:
		set(adapter.NotChecked, "payment payload not readable")
		return
	case !pay.inScope:
		set(adapter.NotChecked, "out of scope: "+pay.why)
		return
	}
	auth, missing, err := pay.authorization()
	switch {
	case missing != "":
		set(adapter.NotPresent, missing+" is absent")
		return
	case err != nil:
		set(adapter.NotChecked, "authorization not readable: "+err.Error())
		return
	case !responseHeld:
		set(adapter.NotPresent, "no settlement response held: no transaction")
		return
	case respErr != nil:
		set(adapter.NotChecked, "settlement response not readable")
		return
	case resp.tx == "":
		set(adapter.NotPresent, "transaction is empty")
		return
	}
	l, why := lookupFor(resp, policy)
	if l == nil {
		set(adapter.NotChecked, why)
		return
	}
	if l.Block == nil || l.TransactionReceipt == nil {
		set(adapter.NotChecked, "no settling block recorded")
		return
	}
	t, err := hexQuantity(l.Block.Timestamp)
	if err != nil {
		set(adapter.NotChecked, "block timestamp: "+err.Error())
		return
	}
	d := fmt.Sprintf("validAfter %s, block timestamp %s, validBefore %s", auth.ValidAfter, t, auth.ValidBefore)
	if auth.ValidAfter.Cmp(t) < 0 && t.Cmp(auth.ValidBefore) < 0 {
		set(adapter.Pass, d)
	} else {
		set(adapter.Fail, d)
	}
}
