// SPDX-License-Identifier: Apache-2.0

package deal

import (
	"strings"
)

// The share gate drops an identifier shorter than four bytes, which would
// match too much as a substring (gateSecrets). An audit copy's reader is no
// party and gets no personal detail at all (evidencebook#1058), so its gate
// also reads each such short value the holder's store holds about someone,
// a name, a word of a name, a phone or handle, as a whole word: "Amy" in
// "Gift for Amy" refuses the copy. A single character is no detail on its
// own and is not read.

// auditCommonWords are short words of a business name that identify no one
// on their own ("Bank of the West", "Kettle Shop Ltd").
var auditCommonWords = map[string]bool{"of": true, "the": true, "and": true, "co": true, "inc": true, "llc": true, "ltd": true, "plc": true, "llp": true, "gmbh": true}

// auditShortWords are the short identifiers of every party the events name.
func auditShortWords(events []sealedEvent) []string {
	var whos []*dealWho
	for _, se := range events {
		e := se.Event
		if e.Open != nil {
			whos = append(whos, &e.Open.Who)
		}
		if e.Intent != nil {
			whos = append(whos, e.Intent.Who)
		}
		if m := e.AgentMove; m != nil {
			whos = append(whos, m.Who)
			for _, o := range m.Options {
				whos = append(whos, o.Who)
			}
		}
		if e.Message != nil {
			whos = append(whos, e.Message.Who)
		}
		if e.Evidence != nil {
			whos = append(whos, e.Evidence.Who)
		}
		if e.Change != nil {
			whos = append(whos, e.Change.Who)
		}
		if e.Snapshot != nil {
			whos = append(whos, e.Snapshot.Who)
		}
		if e.Disclosure != nil {
			whos = append(whos, e.Disclosure.Who)
		}
	}
	seen := map[string]bool{}
	var out []string
	add := func(v string) {
		v = strings.ToLower(strings.TrimSpace(foldText(v)))
		if len(v) >= 2 && len(v) < 4 && !seen[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	for _, w := range whos {
		if w == nil {
			continue
		}
		for _, v := range []string{w.Name, w.Domain, w.Phone, w.Email, w.Payee, w.RelayAddress, w.ProfileID} {
			add(v)
		}
		for _, part := range gateNameWord.FindAllString(strings.ToLower(foldText(w.Name)), -1) {
			if !auditCommonWords[part] {
				add(part)
			}
		}
	}
	return out
}

// auditShortGate refuses an audit copy that carries a short identifier of
// auditShortWords as a whole word, read as dealPageGate reads the copy.
func auditShortGate(data []byte, events []sealedEvent, allowed ...string) error {
	words := auditShortWords(events)
	if len(words) == 0 {
		return nil
	}
	text := removeAllowed([]byte(foldText(string(dealGateText(data, nil)))), allowed)
	return gateCheck(text, nil, words, nil, allowed, 0)
}
