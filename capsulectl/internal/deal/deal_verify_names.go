// SPDX-License-Identifier: Apache-2.0

// Copied from capsule-cli v0.1.0-rc47 (d47ac556), cli/deal_cadence.go and
// cli/deal_profile.go, Apache-2.0 (evidencebook#735): the cadence names and
// the deal profile names the deal-format checks read, which capsule-cli#609
// removed from the core. Profile.DealCadence is a function of the plugin.

package deal

import (
	"crypto/sha256"
	"fmt"
	"time"

	"github.com/action-state-group/agent-action-capsule/go/canonical"
	"github.com/action-state-group/capsule-cli/cli"
)

// A deal profile publishes to its witness on time alone, never on activity.
//
// Each deal is its own log, and none of those logs is ever published: a
// witness that saw them would learn how many deals there are, when each one
// starts, and how many steps each has. Instead the profile keeps one cadence
// log (its log_id). At every tick, and whether or not anything happened,
// `deal tick` cuts each deal's checkpoint locally, builds a fixed-depth
// Merkle tree whose leaves are those checkpoints (each with a fresh random
// salt, at a fresh random position) plus one random filler leaf, appends the
// tree's root as exactly one entry of the cadence log, and publishes the
// cadence log's checkpoint. The witness sees one log that grows by one entry
// per tick on the profile's clock: the volume signal is reduced to the tick
// count, and the timing signal to the cadence.
//
// The chain from a deal's own checkpoint to the witnessed cadence checkpoint
// is the leaf's salt and position, the tree path (always dealCadenceDepth
// hashes, so it says nothing about other deals), the cadence entry's
// inclusion proof, the cadence checkpoint and the witness receipt. The
// holder's own witness check computes it; no bundle written since u225 and
// u230 carries it (an x-deal-v0 copy or a node bundle), since it shows the
// holder's shelf position and cadence (evidencebook#958). `capsulectl deal
// verify --bundle` checks every link of a copy that carries one (rc47's core
// did; the plugin's reader does now).

const (
	// dealCadenceDepth fixes every path at 16 hashes: up to 65535 deals.
	DealCadenceDepth = 16
	// The hosted witness `deal init` points a new profile at unless it is given
	// --no-witness (Steven's ruling, 2026-11-24: witnessed by default). This is
	// the one place the endpoint and its key are defined. The witness is run by
	// Action State, so it is not independent of us. Its key ships in this
	// binary: a user trusts it as far as they trust the release they installed.
	DealDefaultWitness    = "https://witness.agentactioncapsule.org"
	DealDefaultWitnessKey = "39bb654c9dc0afe1c0edef0deffaa69099b8518836c9ba26e0491535840f96b5"
	// The bundle extension that carries a checkpoint's witnessed chain
	// through a cadence log. Its name and fields are generic, so a neutral
	// verifier can read it without knowing what a deal is. Extension names
	// are a registered space; until this one is registered it is a private
	// kind, so it carries the x- prefix.
	DealCadenceExtension = "x-cadence-witness/v0"
	// DealParentWitnessExtension is the neutral kind a witnessed chain
	// travels in (Evidence Bundle -03, "The parent-log-witness/v1
	// Extension"), which the core's AAC verifier checks. It replaced
	// DealCadenceExtension for a chain (evidencebook#737).
	DealParentWitnessExtension = "parent-log-witness/v1"
)

// earlierCadenceExtensions are the names bundles carried the chain under
// before, still read: cadence-witness/v0, and x-deal-cadence-v0 (whose
// chain names its log in deal_log_id).
var earlierCadenceExtensions = []string{"cadence-witness/v0", "x-deal-cadence-v0"}

// cadenceChainOf is the cadence chain a bundle carries, under its name
// (parent-log-witness/v1, with "parent" read back as "cadence", the member
// every reader here takes) or a name bundles carried it under before.
func CadenceChainOf(b map[string]interface{}) map[string]interface{} {
	ext, _ := b["extensions"].(map[string]interface{})
	if block, ok := ext[DealParentWitnessExtension].(map[string]interface{}); ok {
		chain := make(map[string]interface{}, len(block))
		for k, v := range block {
			if k != "parent" {
				chain[k] = v
			}
		}
		chain["cadence"] = block["parent"]
		// The block's presence is the claim, and -03 has a verifier take the
		// extent from `earlier` (a covered checkpoint before the bundle's),
		// never from members it does not define: so a foreign block with no
		// state, or an extent that contradicts its `earlier`, reads as AAC's
		// verifier reads it.
		chain["state"] = "witnessed"
		if _, part := chain["earlier"]; part {
			chain["extent"] = "part"
		} else {
			chain["extent"] = "all"
		}
		return chain
	}
	for _, name := range append([]string{DealCadenceExtension}, earlierCadenceExtensions...) {
		if chain, ok := ext[name].(map[string]interface{}); ok {
			return chain
		}
	}
	return nil
}

type DealCadenceConfig struct {
	Interval, Jitter time.Duration
	PadBucket        uint64
}

func dealCadenceOf(p cli.Profile) (DealCadenceConfig, error) {
	// The checkpoint cadence by default: a tick every 5m, give or take 1m
	// (288 a day). The jitter must stay under half the interval.
	cfg := DealCadenceConfig{Interval: 5 * time.Minute, Jitter: time.Minute, PadBucket: 1}
	var err error
	if p.Cadence.Interval != "" {
		if cfg.Interval, err = time.ParseDuration(p.Cadence.Interval); err != nil {
			return cfg, cli.InputError("cadence.interval must be a duration such as 1h")
		}
	}
	if p.Cadence.Jitter != "" {
		if cfg.Jitter, err = time.ParseDuration(p.Cadence.Jitter); err != nil {
			return cfg, cli.InputError("cadence.jitter must be a duration such as 10m")
		}
	}
	if p.Cadence.PadBucket != 0 {
		cfg.PadBucket = p.Cadence.PadBucket
	}
	switch {
	case cfg.Interval < time.Minute:
		return cfg, cli.InputError("cadence.interval must be at least 1m")
	case cfg.Jitter < 0:
		return cfg, cli.InputError("cadence.jitter must be at least 0")
	case 2*cfg.Jitter >= cfg.Interval:
		// A profile that set a jitter for an earlier default and no interval
		// is told the interval it now gets.
		interval := "cadence.interval is " + ShortDuration(cfg.Interval)
		if p.Cadence.Interval == "" {
			interval = "cadence.interval is not set, so it is the default " + ShortDuration(cfg.Interval)
		}
		return cfg, cli.InputError("cadence.jitter is " + ShortDuration(cfg.Jitter) + " but " + interval + ", and the jitter must be under half the interval: set cadence.interval (for example 1h), or a cadence.jitter under " + ShortDuration(cfg.Interval/2))
	case cfg.PadBucket > 1024:
		return cfg, cli.InputError("cadence.pad_bucket must be at most 1024")
	}
	return cfg, nil
}

// The tree. Leaves, nodes and the empty leaf are domain-separated.
func CadenceLeaf(logID string, size uint64, checkpointSHA256, salt string) ([]byte, error) {
	jcs, err := canonical.JCS(map[string]interface{}{"checkpoint_sha256": checkpointSHA256, "log_id": logID, "salt": salt, "size": cli.Integer(size)})
	if err != nil {
		return nil, err
	}
	h := sha256.Sum256(append([]byte{0x00}, jcs...))
	return h[:], nil
}

func CadenceNode(left, right []byte) []byte {
	h := sha256.New()
	h.Write([]byte{0x01})
	h.Write(left)
	h.Write(right)
	return h.Sum(nil)
}

// cadenceFold recomputes a root from a leaf, its position and its path.
func cadenceFold(leaf []byte, index uint64, path [][]byte) []byte {
	h := leaf
	for level, sibling := range path {
		if (index>>level)&1 == 0 {
			h = CadenceNode(h, sibling)
		} else {
			h = CadenceNode(sibling, h)
		}
	}
	return h
}

// shortDuration writes 1h, 90m, 5m or 30s, never "1h0m0s".
func ShortDuration(d time.Duration) string {
	switch {
	case d%time.Hour == 0:
		return fmt.Sprintf("%dh", d/time.Hour)
	case d%time.Minute == 0:
		return fmt.Sprintf("%dm", d/time.Minute)
	default:
		return d.String()
	}
}

// DealProfile is the x-deal-v0 record block's, and a bundle extension's,
// member name.
const DealProfile = "x-deal-v0"

// DealProfileV1 is the x-deal-v1 profile's, and its bundle extension's, name
// (the profile's PROFILE.md). A v1 bundle names its
// sealed report in this extension, as a v0 bundle does in x-deal-v0; the
// report is always standalone, in x-deal-report/v0.
const DealProfileV1 = "x-deal-v1"
