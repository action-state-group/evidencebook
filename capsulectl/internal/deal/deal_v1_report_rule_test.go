package deal

import (
	"encoding/hex"
	"encoding/json"
	"os"
	"testing"

	aacbundle "github.com/action-state-group/agent-action-capsule/go/bundle"
	"github.com/action-state-group/capsule-cli/cli"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type v1BundleCase struct {
	Name      string                 `json:"name"`
	Report    string                 `json:"report"`
	Extension map[string]interface{} `json:"extension"`
	Want      struct {
		KeyID string `json:"key_id"`
		Error string `json:"error"`
	} `json:"want"`
}

func loadV1BundleCases(t *testing.T) []v1BundleCase {
	t.Helper()
	raw, err := os.ReadFile("../../testdata/deal-v1-report-bundle/vectors.json")
	require.NoError(t, err)
	var vectors struct {
		Cases []v1BundleCase `json:"cases"`
	}
	require.NoError(t, json.Unmarshal(raw, &vectors))
	require.NotEmpty(t, vectors.Cases)
	return vectors.Cases
}

// The vectors are evidencebook's own (testdata/deal-v1/report-bundle), a
// subset: the plugin's rule is xdealv1.VerifyReportBundle, so a report and
// extension it accepts or refuses here are the ones EvidenceBook does.
func TestVerifyV1ReportRule(t *testing.T) {
	for _, c := range loadV1BundleCases(t) {
		t.Run(c.Name, func(t *testing.T) {
			report, err := hex.DecodeString(c.Report)
			require.NoError(t, err)
			ext, err := json.Marshal(c.Extension)
			require.NoError(t, err)
			err = verifyV1Report(report, ext)
			if c.Want.Error == "" {
				assert.NoError(t, err)
				return
			}
			assert.ErrorContains(t, err, c.Want.Error)
		})
	}
}

// v1CopyBundle is a v1 copy's bundle around a vector's report and extension,
// as far as `verify --bundle` reads a standalone report: the holder's seq 1
// agreement as the log's one record (the extension's book carries its pair),
// the checkpoint the report names, and the x-deal-v1 extension beside the
// report in x-deal-report/v0.
func v1CopyBundle(t *testing.T, c v1BundleCase) map[string]interface{} {
	t.Helper()
	reportBytes, err := hex.DecodeString(c.Report)
	require.NoError(t, err)
	var pair map[string]interface{}
	require.NoError(t, json.Unmarshal(reportBytes, &pair))
	input := pair["agent_input"].(map[string]interface{})
	checkpoint := input["deal_checkpoint"].(map[string]interface{})
	agreement := c.Extension["book"].(map[string]interface{})["agreement"].(map[string]interface{})
	record := map[string]interface{}{}
	for k, v := range agreement["record"].(map[string]interface{}) {
		record[k] = v
	}
	record["evidence_record_header"] = agreement["header"]
	return map[string]interface{}{
		"checkpoint": map[string]interface{}{"mmr_size": checkpoint["mmr_size"], "root": checkpoint["root"]},
		"completeness_certificate": map[string]interface{}{
			"log_id": checkpoint["log_id"],
			"memberships": map[string]interface{}{
				record["capsule_id"].(string): map[string]interface{}{"log_coordinates": map[string]interface{}{"seq": float64(1)}},
			},
		},
		"records": []interface{}{record},
		"extensions": map[string]interface{}{
			"x-deal-v1":             c.Extension,
			cli.DealReportExtension: map[string]interface{}{"record": pair["record"], "agent_input": input},
		},
	}
}

func asFile(t *testing.T, v map[string]interface{}) map[string]interface{} {
	t.Helper()
	raw, err := json.Marshal(v)
	require.NoError(t, err)
	out, err := cli.DecodeBundleJSON(raw)
	require.NoError(t, err)
	return out
}

// The core's v1 path with the plugin's rule registered, over a bundle holding
// a real signed report and book (not a stub rule): an accepted report passes
// with no report_rule_not_checked, a refused one fails with report_rule_failed.
// Without the rule the same report only reads report_rule_not_checked.
func TestVerifyBundleRunsTheV1ReportRule(t *testing.T) {
	cases := loadV1BundleCases(t)
	byName := map[string]v1BundleCase{}
	for _, c := range cases {
		byName[c.Name] = c
	}
	for _, name := range []string{"keep-copy", "counterparty-copy", "adjudicator-copy"} {
		t.Run(name, func(t *testing.T) {
			value := asFile(t, v1CopyBundle(t, byName[name]))

			cli.SetDealV1ReportVerifier(nil)
			entry := cli.DealReportEntry(value, aacbundle.VerificationResult{})
			require.Equal(t, "pass", entry["status"], entry)
			assert.Equal(t, []string{cli.DealV1ReportRuleNotChecked}, entry["findings"])

			cli.SetDealV1ReportVerifier(verifyV1Report)
			t.Cleanup(func() { cli.SetDealV1ReportVerifier(nil) })
			entry = cli.DealReportEntry(value, aacbundle.VerificationResult{})
			assert.Equal(t, "pass", entry["status"], entry)
			assert.NotContains(t, entry, "findings")
			assert.NoError(t, cli.CheckDealStandaloneReport(value))
		})
	}

	// A book the report rule refuses fails the bundle, and the failure names the rule.
	for _, name := range []string{"wrong sealed_report", "signed by no party", "a relied-on record missing", "no book"} {
		t.Run("refused: "+name, func(t *testing.T) {
			c := byName[name]
			if name == "no book" {
				// The vector's extension has no usable book to hang a bundle on: use the
				// keep copy's with the book removed.
				c = byName["keep-copy"]
				ext := map[string]interface{}{}
				for k, v := range c.Extension {
					ext[k] = v
				}
				delete(ext, "book")
				bundle := v1CopyBundle(t, c)
				bundle["extensions"].(map[string]interface{})["x-deal-v1"] = ext
				cli.SetDealV1ReportVerifier(verifyV1Report)
				t.Cleanup(func() { cli.SetDealV1ReportVerifier(nil) })
				err := cli.CheckDealStandaloneReport(asFile(t, bundle))
				require.ErrorIs(t, err, cli.ErrBundleInvalid)
				assert.ErrorContains(t, err, cli.DealV1ReportRuleFailed)
				return
			}
			value := asFile(t, v1CopyBundle(t, c))
			cli.SetDealV1ReportVerifier(verifyV1Report)
			t.Cleanup(func() { cli.SetDealV1ReportVerifier(nil) })
			err := cli.CheckDealStandaloneReport(value)
			require.ErrorIs(t, err, cli.ErrBundleInvalid)
			if name == "wrong sealed_report" {
				// The core's own pointer check refuses it before the rule runs.
				assert.ErrorContains(t, err, "sealed_report_unverified")
				return
			}
			assert.ErrorContains(t, err, cli.DealV1ReportRuleFailed)
		})
	}
}
