// SPDX-License-Identifier: Apache-2.0

package evidencebook

import aacwitness "github.com/action-state-group/agent-action-capsule/go/witness"

// VerifyWitnessReceipt verifies a witness receipt for a signed cadence
// checkpoint statement under directory, with the check Cadence.Witnessed
// applies to the holder's own checkpoints, so a reader can check a receipt
// another party sent for its checkpoint (x-deal-v1's entry check,
// evidencebook#773). It returns the check and, when the receipt verified,
// the key id of the directory row it verified under (never one the receipt
// carries). An error means the statement itself does not verify.
func VerifyWitnessReceipt(statement []byte, receipt WitnessReceipt, directory aacwitness.Directory) (WitnessCheck, string, error) {
	check, v, err := verifyWitnessReceipt(statement, receipt, directory)
	return check, v.keyID, err
}
