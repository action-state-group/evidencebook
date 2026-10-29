package book

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"

	"github.com/action-state-group/agent-action-capsule/go/canonical"
	"github.com/action-state-group/agent-action-capsule/go/evidenceplan"
	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook"
)

// `result build --plan` runs a dataset plan (aac spec/evidence-plan-ir-v0.md
// section 5.1) over this book's committed record headers, as a bundle
// discloses them, with aac's own gate (go/evidenceplan), and binds the Result
// to what it ran: the Result's plan_ref and dataset_ref (Result v0 section
// 7.3) are the digests of the plan and of the dataset document the run
// produced (section 6.2), never digests the caller states. The plan and the
// dataset are each sealed as a record of their own, so a bundle rooted on
// the Result can disclose the bytes its digests name: the Result cites both,
// and the dataset cites the plan. The dataset does not cite the records it
// has rows for: its rows name them by seq and header digest, and a closure
// through them would reach every earlier Result a plan over daily Results
// selects, which a report rooted on one Result refuses.

// The record types `result build --plan` seals. record_type is open
// vocabulary; these names are the book's own (evidencebook#107).
const (
	planRecordType    = "evidence_plan"
	datasetRecordType = "evidence_dataset"
)

// planRun is a dataset plan run over this book: the plan and the dataset
// document in canonical (JCS) form with their digests, and the number of
// rows the dataset has.
type planRun struct {
	plan, dataset             []byte
	planDigest, datasetDigest string
	rows                      int
}

// runPlan runs the plan in raw over the book's committed records. It refuses
// a plan over another book, and one whose through_seq is past the book's last
// record: a record committed later would then change the dataset, so the
// same plan would not reproduce it.
func runPlan(ctx context.Context, book *evidencebook.Book, store recordStore, raw []byte) (planRun, error) {
	plan, err := evidenceplan.Decode(raw)
	if err != nil {
		return planRun{}, cli.Hint(cli.ErrInput, "--plan is not one JSON text: "+err.Error())
	}
	records, err := book.Query(ctx, evidencebook.Filter{})
	if err != nil {
		return planRun{}, err
	}
	headers := make([]interface{}, len(records))
	for i, record := range records {
		stored, err := store.GetRecord(ctx, record.RecordID)
		if err != nil {
			return planRun{}, err
		}
		if headers[i], err = evidenceplan.Decode(stored.Header); err != nil {
			return planRun{}, fmt.Errorf("record %s: header: %w", record.RecordID, err)
		}
	}
	dataset, err := evidenceplan.Build(plan, headers)
	var refusal *evidenceplan.Error
	if errors.As(err, &refusal) {
		return planRun{}, cli.Hint(cli.ErrInput, "--plan is refused ("+string(refusal.Reason)+"): "+refusal.Message)
	}
	if err != nil {
		return planRun{}, err
	}
	// Build accepted the plan, so its population node is the first node and
	// carries these params.
	params := plan.(map[string]interface{})["nodes"].([]interface{})[0].(map[string]interface{})["params"].(map[string]interface{})
	bookID, _ := book.Info()
	if params["book_id"] != bookID {
		return planRun{}, cli.Hint(cli.ErrInput, fmt.Sprintf("--plan reads book %q, but this profile's book is %q; a plan runs only over the book it names", params["book_id"], bookID))
	}
	through, _ := params["through_seq"].(json.Number).Int64()
	if last := book.Size(); through < 0 || uint64(through) > last {
		return planRun{}, cli.Hint(cli.ErrInput, fmt.Sprintf("--plan reads through seq %s, but this book's last record is seq %d; a plan reads only records already committed, so the same plan selects the same rows when the book grows", params["through_seq"], last))
	}
	out := planRun{}
	if out.plan, err = canonical.JCS(plan); err != nil {
		return planRun{}, err
	}
	if out.dataset, err = canonical.JCS(dataset); err != nil {
		return planRun{}, err
	}
	if out.planDigest, err = canonical.JSONDigest(plan); err != nil {
		return planRun{}, err
	}
	if out.datasetDigest, err = canonical.JSONDigest(dataset); err != nil {
		return planRun{}, err
	}
	out.rows = len(dataset["rows"].([]interface{}))
	return out, nil
}

// digestRef is a Result's plan_ref or dataset_ref.
func digestRef(digest string) map[string]interface{} {
	return map[string]interface{}{"digest_alg": "SHA-256", "digest": digest}
}

// bindPlan sets the Result's plan_ref and dataset_ref to the run's digests,
// or, when the document already states them, refuses any that differs. With
// no run, a document that states either is refused: the book binds a Result
// only to a plan it ran itself.
func bindPlan(doc map[string]interface{}, run *planRun) error {
	_, hasPlan := doc["plan_ref"]
	_, hasDataset := doc["dataset_ref"]
	if run == nil {
		if hasPlan || hasDataset {
			return cli.InputError("the Result states plan_ref or dataset_ref, but no --plan was given; result build sets both from a plan it runs over this book, and never seals them as stated")
		}
		return nil
	}
	for _, ref := range []struct{ member, digest string }{{"plan_ref", run.planDigest}, {"dataset_ref", run.datasetDigest}} {
		stated, ok := doc[ref.member]
		if !ok {
			doc[ref.member] = digestRef(ref.digest)
			continue
		}
		object, _ := stated.(map[string]interface{})
		if object["digest_alg"] != "SHA-256" || object["digest"] != ref.digest {
			return cli.Hint(cli.ErrInput, fmt.Sprintf("the Result states %s %v, but running --plan over this book gives digest %s; a Result names the plan and dataset this book reproduces", ref.member, object["digest"], ref.digest))
		}
	}
	return nil
}

// sealDocument seals statement, a canonical document, as one record of
// recordType about its own digest with links, or finds the record an
// earlier run sealed for the same bytes and the same links, so a repeat
// appends nothing.
func sealDocument(ctx context.Context, book *evidencebook.Book, recordType string, epistemic evidencebook.EpistemicType, statement []byte, digest string, links []evidencebook.Link) (evidencebook.Record, error) {
	prior, err := book.Query(ctx, evidencebook.Filter{RecordType: recordType, SubjectRef: digest})
	if err != nil {
		return evidencebook.Record{}, err
	}
	for _, record := range prior {
		statement, err := evidenceplan.Decode(record.Header.Statement)
		if err != nil {
			continue
		}
		if sealed, err := canonical.JSONDigest(statement); err == nil && sealed == digest && slices.Equal(record.Header.Links, links) {
			return record, nil
		}
	}
	record, err := book.Append(ctx, evidencebook.Entry{RecordType: recordType, EpistemicType: epistemic, SubjectRef: digest, Links: links, Statement: statement})
	return record, bookError(err)
}

// sealPlanRun seals the plan and the dataset and returns the links the
// Result adds for them.
func sealPlanRun(ctx context.Context, book *evidencebook.Book, run planRun) ([]evidencebook.Link, error) {
	plan, err := sealDocument(ctx, book, planRecordType, evidencebook.ProducerClaim, run.plan, run.planDigest, nil)
	if err != nil {
		return nil, err
	}
	dataset, err := sealDocument(ctx, book, datasetRecordType, evidencebook.DerivedMetric, run.dataset, run.datasetDigest, []evidencebook.Link{{Type: evidencebook.Cites, Target: plan.RecordID}})
	if err != nil {
		return nil, err
	}
	return []evidencebook.Link{{Type: evidencebook.Cites, Target: plan.RecordID}, {Type: evidencebook.Cites, Target: dataset.RecordID}}, nil
}
