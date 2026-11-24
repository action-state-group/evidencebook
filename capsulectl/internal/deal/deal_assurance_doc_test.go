package deal

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The assurance doc names the default witness and the manual countersign
// sequence; it must follow the constant and the commands it describes
// (evidencebook#862).
func TestAssuranceDocNamesTheDefaultWitnessAndTheCountersignSequence(t *testing.T) {
	doc, err := os.ReadFile(filepath.Join("..", "..", "skills", "deal", "ASSURANCE.md"))
	require.NoError(t, err)
	text := string(doc)
	assert.Contains(t, text, DealDefaultWitness)
	assert.Contains(t, text, "--no-witness")
	for _, step := range []string{"countersign request", "deal countersign", "--countersign-directory"} {
		assert.Contains(t, text, step)
	}
	assert.Contains(t, text, "not independent of it")
}
