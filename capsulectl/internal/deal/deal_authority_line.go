package deal

import (
	"slices"
	"strings"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook/dealprofile"
)

// The authority line is what a buyer's agent renders back before a typed deal
// that allows pay opens (Everyday Rule 26: bounded task authority the user
// confirmed, rendered back before reliance). The user's yes to it is
// intent.confirmed; the task authority records the line as shown and a
// commitment to the yes in its asked member, so it is the user's reading that
// is sealed, never the agent's. Sherlock2's wording (evidencebook#839): the
// amount, not the seller, is in the authorized clause until the engine's rule
// 26 compares the seller; moving the seller in is the change that makes it do so.

// unconstrainedSeller is asked.seller when the user named no seller: nothing
// is filled in from an admission pack or the agent's pick.
const unconstrainedSeller = "unconstrained"

// needsAuthorityLine reports whether o is a buyer's typed deal that allows
// pay: the one that must open on the user's yes. A sale's thread is under
// the sale's one task authority and a seller's deal pays nothing.
func (o dealOpen) needsAuthorityLine() bool {
	return o.Records == recordsTyped && o.Sale == "" && o.Intent.PartyRole != dealRoleSeller &&
		slices.Contains(o.Intent.Allowed, "pay")
}

// sellerName is the seller the user picked, as the user knows them.
func (o dealOpen) sellerName() string {
	for _, v := range []string{o.Who.Name, o.Who.Domain, o.Who.Payee, o.Who.Email, o.Who.Phone, o.Who.ProfileID, o.Who.RelayAddress} {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// authorityItem is the item the user asked for, as the baseline records it.
func (o dealOpen) authorityItem() string {
	if o.Intent.Asked.Item != "" {
		return o.Intent.Asked.Item
	}
	return o.Terms.Item
}

// renderAuthorityLine renders the line for the user, in the profile's wording.
func renderAuthorityLine(p cli.Profile, o dealOpen) string {
	var text string
	cli.Speak(map[string]any{}, p, func(w cli.Wording) cli.Narration {
		vals := cli.V{"item": o.authorityItem()}
		if vals["item"] == "" {
			vals["item"] = w.Line("say.authority_line.item_unnamed", nil)
		}
		head := "say.authority_line.head_nomax"
		if m := o.Intent.MaxTotalMinor; m != nil {
			head, vals["max"] = "say.authority_line.head", spokenMoney(*m, o.Terms.Currency)
		}
		parts := []string{w.Line(head, vals)}
		if name := o.sellerName(); name != "" {
			parts = append(parts, w.Line("say.authority_line.seller", cli.V{"seller": name}))
		} else {
			parts = append(parts, w.Line("say.authority_line.no_seller", nil))
		}
		text = strings.Join(append(parts, w.Line("say.authority_line.ask", nil)), " ")
		return silent(w)
	})
	return text
}

// requireConfirmed refuses a buyer's typed pay deal opened without the
// user's yes, so nothing is sealed on the agent's reading of a request.
func (o dealOpen) requireConfirmed() error {
	if !o.needsAuthorityLine() {
		if o.Intent.Confirmed != "" || o.Intent.Rendered != "" {
			return cli.InputError("intent.confirmed and intent.rendered go with a buyer's typed deal that allows pay (--records typed, intent.allowed with pay)")
		}
		return nil
	}
	if strings.TrimSpace(o.Intent.Confirmed) == "" || strings.TrimSpace(o.Intent.Rendered) == "" {
		return cli.InputError("a deal that allows pay opens on the user's yes to what you recorded: run `deal open --authority-line` with this input, show the user its line, and put the line in intent.rendered and their own words answering it in intent.confirmed")
	}
	return nil
}

// requireShown refuses an open whose intent.rendered is not the line this
// input renders: what the user answered is what is recorded, so the line
// they were shown cannot differ from the terms sealed beside it.
func (o dealOpen) requireShown(p cli.Profile) error {
	if o.needsAuthorityLine() && o.Intent.Rendered != renderAuthorityLine(p, o) {
		return cli.InputError("intent.rendered is not the line `deal open --authority-line` shows for this input: show the user that line and open on their answer to it")
	}
	return nil
}

// confirmedAsked is the asked member of a task authority opened on the
// user's yes: the item and the seller the user picked as the baseline holds
// them (a keyed fingerprint, or unconstrained when none was named), the line
// as the user was shown it and the user's answer, each as a commitment.
func confirmedAsked(ta *dealIntent, open *dealOpen, key []byte, commit func(string) (string, error)) (map[string]interface{}, error) {
	asked := map[string]interface{}{"seller": unconstrainedSeller}
	if item := open.authorityItem(); item != "" {
		asked["item"] = item
	}
	if open.Who != (dealWho{}) {
		first := open.Who
		if first.Payee == "" {
			first.Payee = first.Name
		}
		if ids := counterpartyIDs(key, first); len(ids) > 0 {
			asked["seller"] = map[string]interface{}{"fp_alg": dealprofile.TypedFPAlg, "ids": ids}
		}
	}
	var err error
	if asked["said_commitment"], err = commit("confirmed"); err != nil {
		return nil, err
	}
	if ta.Rendered != "" {
		if asked["rendered_commitment"], err = commit("rendered"); err != nil {
			return nil, err
		}
	}
	return asked, nil
}
