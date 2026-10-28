package book

import (
	"fmt"
	"sort"
	"strings"

	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook"
	"github.com/spf13/cobra"
)

// resultShapeNote says what `result open` checked and what the spec still
// leaves open. The Result v0 schema is no longer DRAFT and `result open`
// validates against it (the same gate `result build` and `report build`
// use); the schema also encodes members the spec still marks PROPOSED:
// §4.1's `type: close` claim, §7.1's `coverage_report` and §7.2's
// `coverage_declaration`. `result open` also stands in for a result viewer,
// which does not exist yet, by printing the aggregate and coverage as text.
const resultShapeNote = "Result v0 schema is no longer DRAFT and this document passes it; members the spec still marks PROPOSED (§4.1 type: close, §7.1 coverage_report, §7.2 coverage_declaration) may still change"

// resultAggregate returns the aggregate and its coverage statement, which
// the schema has already required.
func resultAggregate(doc map[string]interface{}) (aggregate, coverage map[string]interface{}) {
	aggregate, _ = doc["aggregate"].(map[string]interface{})
	coverage, _ = aggregate["coverage"].(map[string]interface{})
	return aggregate, coverage
}

func sortedKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func renderResultText(aggregate, coverage map[string]interface{}) string {
	var b strings.Builder
	fmt.Fprintln(&b, "Aggregate:")
	for _, key := range sortedKeys(aggregate) {
		if key == "coverage" {
			continue
		}
		fmt.Fprintf(&b, "  %s: %v\n", key, aggregate[key])
	}
	fmt.Fprintln(&b, "Coverage:")
	for _, key := range sortedKeys(coverage) {
		fmt.Fprintf(&b, "  %s: %v\n", key, coverage[key])
	}
	return b.String()
}

// resultCommands implements `result open`, text mode at minimum: until a
// result viewer exists, this validates the document against the
// Result v0 schema and prints the aggregate/coverage statement itself rather
// than rendering it.
func resultCommands() *cobra.Command {
	group := &cobra.Command{Use: "result", Short: "Inspect a Result v0 document, or seal one into the book"}
	open := &cobra.Command{
		Use:   "open FILE",
		Short: "Validate against the Result v0 schema, then print the aggregate and coverage statement (text mode)",
		Args:  cli.OneArg,
		RunE: func(c *cobra.Command, args []string) error {
			format, _ := c.Flags().GetString("format")
			if format != "text" && format != "json" {
				return cli.InputError("--format must be text or json")
			}
			raw, e := cli.ReadInput(args[0])
			if e != nil {
				return e
			}
			doc, e := decodeResultDocument(raw)
			if e != nil {
				return e
			}
			if e := checkResultSchema(doc); e != nil {
				return e
			}
			aggregate, coverage := resultAggregate(doc)
			notRecomputed := dealReconcileClaimIDs(doc)
			if format == "json" {
				out := map[string]any{"aggregate": aggregate, "note": resultShapeNote}
				if len(notRecomputed) > 0 {
					out["not_recomputed"] = notRecomputed
				}
				return cli.Output(c, out)
			}
			if _, e := fmt.Fprint(c.OutOrStdout(), renderResultText(aggregate, coverage)); e != nil {
				return e
			}
			for _, id := range notRecomputed {
				if _, e := fmt.Fprintf(c.OutOrStdout(), "not recomputed: claim %s cites %s; `result open` recomputes nothing, `deal terms-result --check` checks it\n", id, evidencebook.DealReconcileContractRef); e != nil {
					return e
				}
			}
			_, e = fmt.Fprintln(c.OutOrStdout(), "note: "+resultShapeNote)
			return e
		},
	}
	open.Flags().String("format", "text", "Output format: text or json")
	group.AddCommand(open, resultBuildCommand())
	return group
}

// dealReconcileClaimIDs are the ids of a Result's claims whose contract_ref
// is exactly ec:deal-reconcile@1.
func dealReconcileClaimIDs(doc map[string]interface{}) []string {
	claims, _ := doc["claims"].([]interface{})
	var ids []string
	for _, raw := range claims {
		claim, _ := raw.(map[string]interface{})
		if claim["contract_ref"] == evidencebook.DealReconcileContractRef {
			id, _ := claim["id"].(string)
			ids = append(ids, id)
		}
	}
	return ids
}
