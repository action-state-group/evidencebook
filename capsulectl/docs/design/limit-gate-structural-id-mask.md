# Design note: the spending-limit gate masks ids by JSON structure and field

Issue: https://github.com/action-state-group/evidencebook-deal/issues/62.
Follows https://github.com/action-state-group/evidencebook-deal/pull/60 (5a19f35, fix for #26).
Status: in review (Codex, Kimi's Claude stand-in, Sherlock2). No code until it converges.

## Problem

`dealCeilingGate` (`internal/deal/deal_share_ceiling.go`) refuses a shared copy that states the
user's spending limit (counterparty copies) or floor (every shared copy) as money. #60 stopped
random refusals (#26) by masking the leading digits of a JSON string value that is entirely one
random base64url id (`maskOpaqueIDs`), so `"cose":"8krSil…"` is not read as 8 kr.

The mask finds ids with a pattern, `":\s*"([0-9A-Za-z_-]{16,})"` plus a mixed-case check, on the
folded text (`foldText`). It misses the bound in two ways:

- **Prose that looks like JSON.** With a limit of 8.00, `My "limit": "8USD-Per-Month-Plan-2026".`
  loses its leading `8` to the mask (Ethan accepted this as a declared residual on 2026-10-29).
- **A whole field value shaped like an id.** Any string value of 16 or more mixed-case
  `[0-9A-Za-z_-]` that starts with the bound is masked, whatever field holds it. This is
  reachable: a seller's agent claim `8USD-Per-Month-Plan-ok`, sealed before the user sets a floor
  of 8.00, is opened in the counterparty copy as a whole `text` value, and on d60c732 that copy is
  written; with the claim `lowest is 8 USD` the same copy is refused. The seal-time check
  (`deal.go:1574-1579`) reads only the bounds known when the claim is sealed.

## Where the gate's input comes from

`dealShareGate` is the only caller of `dealCeilingGate` outside tests
(`deal_share_ceiling.go:282`). It has five callers, and each passes one of two kinds of bytes:

| Caller | Bytes | Kind |
| --- | --- | --- |
| `deal_bundle.go:100` (`disclose`) | `json.Marshal(b)` | JSON document |
| `deal_history.go:316` (history copy) | `json.Marshal(b)` | JSON document |
| `deal_terms_seal.go:425` (terms Result bundle) | `json.Marshal(b)` | JSON document |
| `deal_evidence.go:742` (evidence-request copy) | `canonical.JCS(...)` | JSON document |
| `deal.go:2562` (`report --html`) | the emitted page | HTML page |

`dealShareGate` passes `dealGateText(data)` (`deal_share_gate.go:94`), which:

- for a copy that does not start with an HTML doctype, returns the whole copy less the two vendored
  scripts (`dealWithoutVendored`); for the four JSON callers that is the JSON document;
- for a page, returns the markup outside every `<script>` and `<style>`, plus the JSON value of each
  of the two data scripts (`window.__BUNDLE__ =` and `globalThis.capsulectlDealPage =
  Object.freeze(`), each cut out with `json.Decoder` (`deal_share_gate.go:113-125`). A data script
  that does not decode is kept whole as text.

So the real JSON regions are already delimited: the whole copy for the four bundle callers, and the
two data-script values of a page. Everything else is prose: page markup (the title and the
emitter's shell), a data script that does not decode, and any copy that is not valid JSON.

`dealCeilingGate` then folds the text, takes out the deal's own ids (`removeWholeToken`) and every
whole hex token of 16 or more (`removeHexTokens`), and only then masks (`deal_share_ceiling.go:
221-243`). Folding decodes percent-escapes, HTML character references and `\uXXXX` escapes,
repeatedly (`deal_share_fold.go:40-63`). A literal `"` in user text is written `\"` by
`json.Marshal` and is not decoded, which is why `{"note":"My \"limit\": \"8USD…\"."}` is refused
today. What folding turns into a quote is user text that holds an escaped quote: `&#34;` or
`&quot;`, `%22`, or the six characters `\u0022`. Text like that, inside any string field, reaches
the mask looking like JSON.

## Design

Mask only the value of a `cose` field, found by the JSON structure of the copy's real JSON
regions, and only when it is still a whole id after the steps that run before the mask today.

1. **Regions.** `dealGateText` gains a hook for the JSON regions it already cuts out:
   `dealGateText(page, jsonValue func([]byte) []byte)`. It applies `jsonValue` to the raw bytes of
   each data-script value it decodes, and to a non-page copy only when the whole copy (less the
   vendored scripts) is valid JSON (`json.Valid`). Markup, a data script that does not decode, and
   a non-JSON copy are returned as now. `dealPageGate` passes `nil` and reads exactly what it reads
   today.
2. **Field.** The hook walks each region with `json.Decoder.Token` and masks the string value
   of every object member whose key, as decoded, is exactly `cose`, at any depth. A key is known by
   its position (the first string after `{` or after a member), not by its bytes, so a key such as
   `"\"cose"` or `"prefix\"cose"` is not `cose`, and a `cose` value that is an object or an array
   is not masked. The value is masked only when its raw bytes are the decoded value between two
   quotes (no escapes); `Decoder.InputOffset` gives its end. Two regex drafts of this rule each
   missed a case in review (prose imitating JSON, then a key holding an escaped quote), so the
   rule is a token walk, not a pattern.
   `cose` is the one base64url field in the counterparty copy dumped for this note: the checkpoint
   (`evidencebook` `substrate_cll.go:332`); a cadence chain's checkpoint (`cadence_chain.go:79`)
   has the same key. Every other string value in that copy is hex, a date, a word or prose, and
   its extensions are `x-cadence-witness/v0`, `x-deal-report/v0` and `x-deal-v0` only.
   `evidencebook` also writes selected payloads as base64url (`bundle.go:300`, under
   `extensions["evidencebook/payloads"]`, `bundle.go:317-318`); that extension is absent from the
   dumped copy, and the guard test below settles whether any other copy kind carries it.
3. **Eligibility, in today's order.** A `cose` value is masked only if it is entirely
   `[0-9A-Za-z_-]` with an upper-case and a lower-case letter, and it is unchanged when the deal's
   own ids and the whole hex tokens are taken out of it. That keeps the order main applies today
   (fold, own ids, hex, then mask): a value that those steps would break up is not a whole id by
   the time main masks, so this design does not mask it either. Only its leading digits become a
   letter.
4. **Caller.** `dealShareGate` passes the raw copy to `dealCeilingGate`, which calls
   `dealGateText(data, mask)` itself, then folds and checks as now. The post-fold mask call
   (`deal_share_ceiling.go:243`) is removed.

The money reader (`deal_money_text.go`), the bound-name checks and `removeHexTokens` do not change.

### Keeping the field list right

A base64url field the list misses brings back #26's random refusals for that field (a false
positive, never a missed bound). The code PR adds a guard test that builds every copy kind the
suite can produce (counterparty and adjudicator bundles, the history copy, the terms Result bundle,
the evidence-request copy and the counterparty page, with a cadence witness configured), walks
each JSON region, and lists every mixed-case id-shaped string value under a key other than `cose`.
That list is a signal to investigate, not a rule to extend the mask. A field joins the mask only
when the producer code shows it holds machine-encoded bytes only (a base64url encoding of a
statement or payload, cited in the PR), never text a person or an agent wrote. The seller-claim
case (`text`) stays a refusal test, so a free-text field can't join by mistake. If the guard meets
`evidencebook/payloads`, its values qualify (`bundle.go:300`).

### Considered and not chosen

- **Masking by shape anywhere in a JSON value (the first draft of this note).** It misses the
  reachable seller-claim case above, since a claim's whole text can be id-shaped.
- **Moving the mask before folding with no JSON check** (one reviewer's simpler variant). It closes
  the escape cases but stays a pattern on text whose structure is unknown, which is the class #62
  exists to end.
- **A regex for the field on the raw JSON.** See step 2: it matched a key holding an escaped quote.

## Prose regions

Prose is never masked: page markup, the text of an undecodable data script, a copy that is not
valid JSON, every string value other than a `cose` value, and every key. The bound-name checks keep
reading the whole text, keys included.

## Residuals

Closed (missed on main, refused with this design):

- `My "limit": "8USD-Per-Month-Plan-2026".` as a non-JSON copy (the #62 repro), and in page markup.
- The same prose inside a JSON string written with `&#34;`, `&quot;`, `%22` or `\u0022`, in a
  bundle or in a page's bundle.
- A whole field value shaped like an id that starts with the bound (`{"note":"8USD-Per-Month-Plan-
  2026"}`, the seller claim `{"text":"8USD-Per-Month-Plan-ok"}`).

Left open, declared:

- **A `cose` value that states the bound.** A `cose` value is a COSE statement this tool or a
  verifying countersigner encoded; it is never user text.
- **False positives (the gate refuses more, never less):** a `cose` id in a copy that is not valid
  JSON, in an undecodable data script, or followed by prose in a non-JSON copy, is no longer
  masked and may be read as money, as before #60. No caller writes such a copy: the four bundle
  callers write JSON and the emitter writes valid JSON data scripts. The same holds for an id
  written with escapes (`\u0038krSil…`) or non-ASCII digits, and for a base64url field the guard test
  has not met.

## Test vectors

Limit is in minor units, USD, buyer, counterparty audience unless noted. "main" is
`dealCeilingGate(dealGateText(x))` on d60c732; "design" is a scratch prototype of this note on
d60c732 (`protoGateText` with the `cose` token-walk hook, and the gate without the post-fold
mask). R =
refused, P = passes.

Random ids (must pass), from `TestARandomIdIsNotTheLimit` and #60's rounds:

| Limit | Input | main | design |
| --- | --- | --- | --- |
| 800 | `{"cose":"8krSiljccsfh-Gc4lpkxi8m4j8wl"}` | P | P |
| 800 | `{"cose":"8krSiljccsfhGc4lpkxi8w"}` (R3: 22 characters) | P | P |
| 800 | `{"cose":"8krSiljccsfhGcAlpkxiAw"}` (R4: one digit group) | P | P |
| 800 | `{"cose":"8kR2HyiCRXnmH-sExilKR7VFhmKmkFs-A8P1Z2q_z_R"}` | P | P |
| 800 | `{"cose":"8gbp-Qlhh6xzsywqlv3kuyf8b1xtzkQ"}` | P | P |
| 800 | `{"cose":"8usd_K3j2h1xxxxxxxxxxxxxxxxxx"}`, `{"cose":"8eur-Ab3xxxxxxxxxxxxxxxxxxxxxxx"}`, `{"cose":"8chf-Xz9xxxxxxxxxxxxxxxxxxxxxxxx"}` | P | P |
| 800 | page whose bundle is `{"checkpoint":{"cose":"8krSiljccsfh-Gc4lpkxi8m4j8wl"}}` | P | P |
| 800 | `{"a":{"cose":"8krSiljccsfh-Gc4lpkxi8m4j8wl"},"b":"x"}` | P | P |
| 800 | `{"cose" : "8krSiljccsfh-Gc4lpkxi8m4j8wl"}` (whitespace before the colon: main's pattern misses it; no producer writes it) | R | P |
| 800 | `{"k":"8A3Cad8E9f0a1b2c3d4"}` and `{"cose":"8A3Cad8E9f0a1b2c3d4"}` (mixed-case hex: blanked by `removeHexTokens`) | P | P |

Bound stated (must refuse), from `TestTheLimitInOrBesideALongRunIsStillRefused` and the rounds:

| Limit | Input | main | design |
| --- | --- | --- | --- |
| 800 | `my limit is $8.00`, `8kr`, `limit 8usd-ish`, `my-limit-is-$8` | R | R |
| 800 | `limit 8.00 Zq3-Tr9xYb_Lm4NpWk7VcHd2Fg5Js8Aa`, `Zq3-Tr9xYb_Lm4NpWk7VcHd2Fg5Js8Aa limit 8.00` | R | R |
| 800 | `Lowest-I-Will-Take-Is-Exactly-8-Dollars-Thanks-ok limit is 8 usd` | R | R |
| 800 | `Max_Budget_Limit_FY2026Q4: 8` (R3) | R | R |
| 800 | `My-Personal-Spending-Limit-2026 is 8`, `My-Absolute-Spending-Limit-Is-8.50 and 8.00` (R3) | R | R |
| 800 | `8 usd_Settlement_Ref_A1b2c3d4`, `8USD-Per-Month` | R | R |
| 800 | `{"note":"8krSiljccsfh-Gc4lpkxi8m4j8wl and my limit is 8"}` | R | R |
| 800 | `{"note":"my limit is 8 USD"}`, `{"note":"8USD-Per-Month-Plan-2026-Ok limit"}` | R | R |
| 800 | `{"8krSiljccsfh-Gc4lpkxi8m4j8wl":"limit 8 usd"}` (id as a key) | R | R |
| 800 | `My limit is "8USD-Per-Month-Plan-2026".` (R5) | R | R |
| 800 | `{"note":"My limit is \"8USD-Per-Month-Plan-2026\"."}` | R | R |
| 800 | `["8USD-Per-Month-Plan-2026"]` | R | R |
| 800 | `{"max_total_minor":800}`, `{"x_max_total_minor_Zq3-Tr9xYb_Lm4NpWk7VcHd2Fg5Js8Aa":1}` | R | R |
| floor 170000, seller | `{"x_min_total_minor_Zq3-Tr9xYb_Lm4NpWk7VcHd2Fg5Js8Aa":1}` | R | R |
| 850 | `$8.50USD-Per-Month-Plan-2026` (R4) | R | R |
| 80000 | `USD700-USD800`, `800USD-900USD`, `800usd-ish` (R1, R2) | R | R |
| 800000 | `$8k-per-month`, `{"pay":"8k-4Qwxyz"}`, `USD7k-USD8k`, `USD8k-per-month`, `my limit is 8k-per-month` (R1, R2) | R | R |
| 80000 | `my limit is 800USD- final` (R2) | R | R |
| 800 | `$8kr`, `8 kr` (R2) | R | R |
| 80000 | `Max_Budget_Limit_FY2026Q4: 800`, `800 USD_Settlement_Ref_A1b2c3` (R3) | R | R |
| 800 | `{"note":"My \"limit\": \"8USD-Per-Month-Plan-2026\"." , }` (invalid JSON: read whole, no mask) | R | R |
| 800 | `{"note":"8USD-Per-Month-aaaaaaaaaaaaaaaa"}`, `{"cose":"8USD-Per-Month-aaaaaaaaaaaaaaaa"}` (the hex tail is blanked first, so the value is no id) | R | R |

The #62 residual and the cases this note adds:

| Limit | Input | main | design |
| --- | --- | --- | --- |
| 800 | `My "limit": "8USD-Per-Month-Plan-2026".` (R6, the #62 repro) | P | R |
| 800 | page whose title is `My &#34;limit&#34;: &#34;8USD-Per-Month-Plan-2026&#34;.` | P | R |
| 800 | page whose title is `My "limit": "8USD-Per-Month-Plan-2026".` | P | R |
| 800 | `{"note":"My \\u0022limit\\u0022: \\u00228USD-Per-Month-Plan-2026\\u0022."}` | P | R |
| 800 | `{"note":"My &#34;limit&#34;: &#34;8USD-Per-Month-Plan-2026&#34;."}` | P | R |
| 800 | `{"note":"My &quot;limit&quot;: &quot;8USD-Per-Month-Plan-2026&quot;."}` | P | R |
| 800 | `{"note":"My %22limit%22: %228USD-Per-Month-Plan-2026%22."}` | P | R |
| 800 | page whose bundle is the `&#34;` note above, and the `%22` note above | P | R |
| 800 | `{"note":"8USD-Per-Month-Plan-2026"}`, and as a page's bundle | P | R |
| 800 | `{"text":"8USD-Per-Month-Plan-ok"}` | P | R |
| 800 | `{"cose":"8krSiljccsfh-Gc4lpkxi8m4j8wl"} trailing prose` (not JSON: declared false positive) | P | R |
| 800 | `{"\"cose":"8USD-Per-Month-Plan-2026"}`, `{"prefix\"cose":"8USD-Per-Month-Plan-ok"}` (keys that are not `cose`) | P | R |
| 800 | `{"cose":{"note":"8USD-Per-Month-Plan-2026"}}` (a `cose` object is not masked) | P | R |
| 800 | `{"cose":["8USD-Per-Month-Plan-2026"]}` | R | R |
| 800 | `{"cose":"\u0038krSiljccsfh-Gc4lpkxi8m4j8wl"}` (escaped id: declared false positive) | P | R |

End to end (the code PR adds it): a seller deal opened with no floor, an agent claim
`8USD-Per-Month-Plan-ok`, then an intent note setting `min_total_minor` 800; `disclose --share
counterparty` writes the copy on d60c732 and must refuse with this design. The control claim
`lowest is 8 USD` is refused on both.

The code PR adds every row above to the gate's tests, the false-positive row asserting its outcome
with a comment, so a later change to it is deliberate.

Real-copy fuzz, kept as a test: the counterparty bundle and page from the `openShortMerchantDeal`
fixture, `checkpoint.cose` replaced by a random 400-character base64url value starting with `8`
(half of them `8kr`), fixed seed, against a limit of 8.00. Acceptance: 0 refusals.

## Acceptance

- Every vector above has the "design" outcome, and the end-to-end seller case is refused.
- These tests pass unchanged: `TestARandomIdIsNotTheLimit`, `TestRandomBase64urlIdsNeverStateTheLimit`,
  `TestTheLimitInOrBesideALongRunIsStillRefused`, `TestDealCeilingGate`
  (`deal_share_ceiling_test.go`), `TestTheCeilingGateReadsPastIDsAndDigests` and
  `TestTheCeilingGateBlanksHexTokens` (`deal_money_text_test.go`), and the page gate's tests
  (`dealPageGate` reads the same bytes as before).
- The real-copy fuzz test shows 0 refusals, and the guard test finds no id-shaped field but `cose`.
- The comment on the mask names only the declared residuals.

## Prototype

Scratch files on d60c732, not committed: `zz_proto62_test.go` (the hook, the `cose` token walk and
the gate without the post-fold mask), `zz_fuzz62_test.go`, and the probes. Real-copy fuzz, fixed seed
62, limit 8.00:

| Copy | Rounds | d60c732 | design | no mask at all |
| --- | --- | --- | --- | --- |
| counterparty bundle | 20,000 | 0 | 0 | 9,953 |
| counterparty page | 2,000 | 0 | 0 | 1,028 |

The "no mask" column shows the harness exercises the #26 failure (half the rounds start `8kr`).

## Worth doing

Yes. The whole-value case is reachable through a seller's agent claim and states the floor to the
buyer; this design closes it and the escape cases with a hook on `dealGateText`, a token walk of
about 50 lines and its eligibility check, plus tests.
