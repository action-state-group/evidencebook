package deal

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/action-state-group/agent-action-capsule/go/canonical"
	"github.com/action-state-group/capsule-cli/cli"
	"github.com/spf13/cobra"
)

// dealDisclosuresCommand reads back the disclosure records the deal's shares
// put on record (recordShare), as the holder's own store holds them. It is the
// holder's reader only: the output goes to stdout, carries each record's
// nonce, recipient and withheld records, and feeds no share, page or bundle.
func dealDisclosuresCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "disclosures", Short: "Your deal's sealed disclosure records, for your own reading (read-only)", Long: "The sealed disclosure record of each share of the deal, as your store holds it, each with its digest and its sequence on the deal's disclosure log. For your own reading only: never put the output in a share or a file you send. Read-only: seals and writes nothing, and reads the log not at all.", Args: cli.NoArgs, RunE: func(c *cobra.Command, _ []string) (err error) {
		p, err := cli.Selected(c)
		if err != nil {
			return err
		}
		dealID, _ := c.Flags().GetString("deal")
		if dealID == "" {
			return cli.InputError("--deal is required: a deal id as printed by `deal open`")
		}
		if !cli.DealIDPattern.MatchString(dealID) {
			return cli.InputError("--deal must be a deal id as printed by `deal open`: deal- followed by 16 hex characters")
		}
		digest, _ := c.Flags().GetString("record")
		if digest != "" && !contentDigestPattern.MatchString(digest) {
			return cli.InputError("--record must be a disclosure record digest: 64 lowercase hex characters, as a share prints in disclosure_record")
		}
		s, err := openDealSessionRead(c.Context(), p, dealID)
		if err != nil {
			return err
		}
		defer func() { err = errors.Join(err, s.close()) }()
		out, err := s.disclosures(c.Context(), dealID, digest)
		if err != nil {
			return err
		}
		return cli.Output(c, out)
	}}
	cmd.Flags().String("deal", "", "Deal id")
	cmd.Flags().String("record", "", "Only the record with this digest (a share's disclosure_record)")
	return cmd
}

// disclosures lists the deal's stored records in the order they were sealed,
// or the one with the given digest. Each record is the object recordShare
// stored. A row still at sequence 0 was never confirmed on the log (recordShare
// inserts before it appends), so it is not listed, as deal_requests rows are
// not (exchangeRows). Its JCS digest is
// recomputed and must be the digest stored with it (the one its share sealed);
// the log itself is not re-read.
func (s *dealSession) disclosures(ctx context.Context, dealID, only string) (_ map[string]any, err error) {
	q, args := `SELECT n, record_digest, cll_sequence, record FROM deal_disclosures WHERE deal_id=? AND cll_sequence>0 ORDER BY n`, []any{dealID}
	if only != "" {
		q, args = `SELECT n, record_digest, cll_sequence, record FROM deal_disclosures WHERE deal_id=? AND cll_sequence>0 AND record_digest=? ORDER BY n`, append(args, only)
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, err
	}
	defer func() { err = errors.Join(err, rows.Close()) }()
	list := []map[string]any{}
	for rows.Next() {
		var n, seq int64
		var digest, raw string
		if err := rows.Scan(&n, &digest, &seq, &raw); err != nil {
			return nil, err
		}
		var record map[string]any
		if err := json.Unmarshal([]byte(raw), &record); err != nil {
			return nil, cli.ErrConflict
		}
		again, err := canonical.JSONDigest(record)
		if err != nil || again != digest {
			return nil, cli.ErrConflict
		}
		list = append(list, map[string]any{"n": n, "disclosure_record": digest, "sequence": seq, "log_id": dealDisclosureLogID(dealID), "record": record})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if only != "" && len(list) == 0 {
		return nil, cli.InputError("this deal has no disclosure record with digest " + only)
	}
	return map[string]any{"deal_id": dealID, "disclosures": list}, nil
}
