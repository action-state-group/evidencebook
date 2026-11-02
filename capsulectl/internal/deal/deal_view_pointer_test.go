package deal

import (
	"os/exec"
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDealPointerTo runs deal-view.js's dealPointerTo on synthetic records,
// since no deal verb seals a link from their half to the holder's records yet
// (evidencebook-deal#95): the pass path cannot be produced from a real flow.
func TestDealPointerTo(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("node is not installed")
	}
	src := string(mustRead(t, "assets/deal-view.js"))
	fn := regexp.MustCompile(`(?s)const dealPointerTo = .*?\n};\n`).FindString(src)
	require.NotEmpty(t, fn, "dealPointerTo not found in deal-view.js")
	script := fn + `
const rec = (book, links) => ({ evidence_record_header: { book_id: book, links } });
const out = [
  dealPointerTo(["a", "b"], [rec("T", [{ type: "acknowledges", target: "b" }])], "T"),
  dealPointerTo(["a"], [rec("T", [{ type: "rebuts", target: "a" }])], "T"),
  dealPointerTo(["a"], [rec("T", [{ type: "counterparty_half", target: "a" }])], "T"),
  dealPointerTo(["a"], [rec("T", [{ type: "rebuts", target: "a" }, { type: "acknowledges", target: "a" }])], "T"),
  dealPointerTo(["a"], [rec("T", [{ type: "acknowledges", target: "zzz" }])], "T") === undefined,
  dealPointerTo(["a"], [rec("OTHER", [{ type: "acknowledges", target: "a" }])], "T") === undefined,
  dealPointerTo(["a"], [{}, { evidence_record_header: { book_id: "T" } }], "T") === undefined,
  dealPointerTo(["a"], undefined, "T") === undefined,
];
console.log(JSON.stringify(out));`
	got, err := exec.Command(node, "-e", script).CombinedOutput()
	require.NoError(t, err, string(got))
	assert.JSONEq(t, `["acknowledges","rebuts","other","acknowledges",true,true,true,true]`, string(got))
}
