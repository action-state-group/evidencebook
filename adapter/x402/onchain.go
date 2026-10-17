// SPDX-License-Identifier: Apache-2.0

package x402

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/action-state-group/evidencebook/adapter"
)

// TxLookup is a recorded transaction lookup, supplied by the relying party
// under TrustPolicy.Recorded["x402.tx_lookup"]. It is the relying party's
// own observation of the chain, made earlier through Source; the adapter
// never makes the call.
type TxLookup struct {
	Source      string `json:"source"`
	Network     string `json:"network"`
	Transaction string `json:"transaction"`
	// Status is "success", "failed" or "absent" (no such transaction).
	Status string `json:"status"`
	// Final is false when the source did not consider the transaction final.
	Final bool `json:"final"`
	// BlockTimestamp is the settling block's timestamp, decimal seconds.
	BlockTimestamp string  `json:"block_timestamp"`
	Logs           []TxLog `json:"logs"`
}

// TxLog is one decoded event log of the transaction.
type TxLog struct {
	Address string `json:"address"`
	// Event is "Transfer" (From, To, Value) or "AuthorizationUsed"
	// (Authorizer, Nonce).
	Event      string `json:"event"`
	From       string `json:"from,omitempty"`
	To         string `json:"to,omitempty"`
	Value      string `json:"value,omitempty"`
	Authorizer string `json:"authorizer,omitempty"`
	Nonce      string `json:"nonce,omitempty"`
}

// CodeLookup is a recorded code lookup at an address, supplied under
// TrustPolicy.Recorded["x402.code_lookup"]. Code "0x" means no code: an
// externally owned account.
type CodeLookup struct {
	Source  string `json:"source"`
	Network string `json:"network"`
	Address string `json:"address"`
	Code    string `json:"code"`
}

func recorded[T any](policy adapter.TrustPolicy, key string) (*T, error) {
	raw, ok := policy.Recorded[key]
	if !ok {
		return nil, nil
	}
	if _, err := decodeStrict(raw); err != nil {
		return nil, fmt.Errorf("recorded %s: %v", key, err)
	}
	var v T
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, fmt.Errorf("recorded %s: %v", key, err)
	}
	return &v, nil
}

func lookupSource(policy adapter.TrustPolicy) string {
	if l, err := recorded[TxLookup](policy, RecordedTxLookup); err == nil && l != nil && l.Source != "" {
		return "recorded lookup from " + l.Source
	}
	return "none (offline)"
}

// recordedNoCode reports whether a recorded code lookup shows no code at a.
func recordedNoCode(policy adapter.TrustPolicy, a address) (bool, string) {
	c, err := recorded[CodeLookup](policy, RecordedCodeLookup)
	if err != nil || c == nil {
		return false, ""
	}
	got, err := parseAddress(c.Address)
	if err != nil || got != a {
		return false, ""
	}
	return c.Code == "0x", c.Source
}

// checkSettlementOnchain applies the declaration's §6.4 to a recorded
// lookup, and the EIP-3009 validity window at the settling block's
// timestamp (ruling Q3). Offline, without a recording for this transaction,
// it is NOT_CHECKED.
func checkSettlementOnchain(o *out, resp *response, pay *payment, policy adapter.TrustPolicy) {
	method := methodSettlementOnchain(resp.network, lookupSource(policy))
	set := func(st adapter.Status, d string) { o.native(RowSettlementOnchain, st, method, d) }
	if resp.tx == "" {
		d := "transaction is empty"
		if success, _ := resp.root.boolean("success"); !success {
			reason, _ := resp.root.str("errorReason")
			d += fmt.Sprintf("; the response states success false, errorReason %q", reason)
		}
		set(adapter.NotPresent, d)
		return
	}
	l, err := recorded[TxLookup](policy, RecordedTxLookup)
	if err != nil {
		set(adapter.NotChecked, err.Error())
		return
	}
	if l == nil || !strings.EqualFold(l.Transaction, resp.tx) || l.Network != resp.network {
		set(adapter.NotChecked, "offline: no recorded lookup of "+resp.tx+" on "+resp.network)
		return
	}
	switch l.Status {
	case "absent":
		set(adapter.Fail, "the transaction is absent on "+l.Network)
		return
	case "failed":
		set(adapter.Fail, "the transaction failed on "+l.Network)
		return
	case "success":
	default:
		set(adapter.Inconclusive, fmt.Sprintf("recorded status %q", l.Status))
		return
	}
	if !l.Final {
		set(adapter.Inconclusive, "the source did not consider the transaction final")
		return
	}
	if pay == nil || !pay.inScope {
		set(adapter.Inconclusive, "no in-scope payment payload held: the events cannot be matched to an authorization")
		return
	}
	auth, missing, aerr := pay.authorization()
	assetStr, _ := pay.accepted.str("asset")
	asset, perr := parseAddress(assetStr)
	if missing != "" || aerr != nil || perr != nil {
		set(adapter.Inconclusive, "the authorization or accepted.asset is not readable: the events cannot be matched")
		return
	}
	var transfer, used bool
	for _, lg := range l.Logs {
		emitter, err := parseAddress(lg.Address)
		if err != nil || emitter != asset {
			continue
		}
		switch lg.Event {
		case "Transfer":
			f, e1 := parseAddress(lg.From)
			t, e2 := parseAddress(lg.To)
			v, e3 := parseUint256(lg.Value)
			if e1 == nil && e2 == nil && e3 == nil && f == auth.From && t == auth.To && v.Cmp(auth.Value) == 0 {
				transfer = true
			}
		case "AuthorizationUsed":
			a, e1 := parseAddress(lg.Authorizer)
			n, e2 := parseBytes32(strings.ToLower(lg.Nonce))
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
	ts, err := parseUint256(l.BlockTimestamp)
	if err != nil {
		set(adapter.Inconclusive, "no usable block timestamp recorded: "+err.Error())
		return
	}
	if !(auth.ValidAfter.Cmp(ts) < 0 && ts.Cmp(auth.ValidBefore) < 0) {
		fails = append(fails, fmt.Sprintf("block timestamp %s is outside (validAfter %s, validBefore %s)", ts, auth.ValidAfter, auth.ValidBefore))
	}
	if len(fails) > 0 {
		set(adapter.Fail, strings.Join(fails, "; "))
		return
	}
	set(adapter.Pass, fmt.Sprintf("success, final, Transfer and AuthorizationUsed match, block timestamp %s within the window", ts))
}
