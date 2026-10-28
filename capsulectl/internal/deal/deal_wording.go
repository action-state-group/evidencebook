package deal

import (
	_ "embed"

	"github.com/action-state-group/capsule-cli/cli"
)

// dealWordingBytes is the deal plugin's own built-in wording pack: every key
// its commands speak that the core's pack does not hold (capsule-cli#428).
// It is registered beside the core's pack, so this process speaks with both,
// and the plugin's handshake carries it, so the core checks a pack pinned on
// a deal profile against these keys too.
//
//go:embed assets/wording/capsulectl-deal-en.json
var dealWordingBytes []byte

func init() { cli.RegisterWording(dealWordingBytes) }
