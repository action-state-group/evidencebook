package deal

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

// A deal page opened where WebCrypto is missing (a file: or plain http page is
// not a secure context) must not read a valid bundle as did-not-verify. The
// runtime checks the records, graph and coverage with its plain SHA-256; the
// deal view cannot check the report's Ed25519 signature there, so it says so
// and shows none of the report. With WebCrypto the same page shows it.
// (capsule-viewer/go v0.9.0 exports no switch to turn the SHA-256 fallback
// off, so the "unable to verify in this viewer" page is not reachable here.)
func TestADealPageWithoutWebCrypto(t *testing.T) {
	run := dealPageUnderNode(t)

	bare := run("no-crypto")
	assert.Equal(t, "undefined", bare["crypto"])
	assert.Equal(t, "false", bare["ed25519"])
	assert.Equal(t, "pass,pass,pass", bare["checks"], "graph closure, coverage and membership verify with the plain SHA-256")
	assert.Equal(t, "ok", bare["records"], "every record verifies")
	assert.Empty(t, bare["findings"])
	assert.Equal(t, "ok", bare["disclosures"])
	assert.Equal(t, "module", bare["presentation"])
	assert.Equal(t, "⚠️ This viewer cannot check the report's signature here. Run capsulectl verify on the file before relying on it.", bare["refusal"])
	assert.Empty(t, bare["scope"], "no report text is shown unchecked")

	secure := run("")
	assert.Equal(t, "pass,pass,pass", secure["checks"])
	assert.Equal(t, "true", secure["ed25519"])
	assert.Empty(t, secure["refusal"])
	assert.NotEmpty(t, secure["scope"])
}
