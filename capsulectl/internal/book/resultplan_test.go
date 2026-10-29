package book

import (
	"encoding/json"
	"testing"

	"github.com/action-state-group/agent-action-capsule/go/canonical"
	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/evidencebook"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// testPlan is a dataset plan over the profile's book: its own_capsule
// records through seq through, committed on day0's UTC day, then each
// predicate in where.
func testPlan(bookID string, through uint64, where ...map[string]any) map[string]any {
	node := func(id, operator string, params map[string]any, input string) map[string]any {
		n := map[string]any{
			"id": id, "family": "traditional", "operator": operator, "tier": "recomputed",
			"classification": "CLOUD_SAFE", "contract_ref": testContract, "inputs": []any{}, "params": params,
		}
		if input != "" {
			n["inputs"] = []any{map[string]any{"by": "node", "node_id": input}}
		}
		return n
	}
	nodes := []any{
		node("population", "traditional.population", map[string]any{"book_id": bookID, "through_seq": through, "record_types": []any{recordTypeOwn}}, ""),
		node("window", "traditional.window", map[string]any{"field": "committed_at", "from": "2026-09-24", "to": "2026-09-24"}, "population"),
	}
	if len(where) > 0 {
		predicates := make([]any, len(where))
		for i, w := range where {
			predicates[i] = w
		}
		nodes = append(nodes, node("filter", "traditional.filter", map[string]any{"where": predicates}, "window"))
	}
	return map[string]any{
		"header": map[string]any{
			"ir_version": "evidence-plan-ir-v0", "contract_ref": testContract, "planner_id": "local:test-planner",
			"created_at": "2026-09-26T00:00:00Z", "replay_seed": "test-seed-1",
		},
		"nodes": nodes,
	}
}

// bookIdentity is the profile's book id and its last seq.
func bookIdentity(t *testing.T, b resultBook) (string, uint64) {
	t.Helper()
	var id string
	var size uint64
	withBook(t, b.profile, func(book *evidencebook.Book) {
		id, _ = book.Info()
		size = book.Size()
	})
	return id, size
}

func planDigests(t *testing.T, b resultBook, plan map[string]any) planRun {
	t.Helper()
	raw, err := json.Marshal(plan)
	require.NoError(t, err)
	var run planRun
	opened, err := openBook(t.Context(), b.profile, false)
	require.NoError(t, err)
	defer func() { require.NoError(t, opened.release()) }()
	run, err = runPlan(t.Context(), opened.book, opened.store, raw)
	require.NoError(t, err)
	return run
}

// Acceptance 1 (#107): a plan-driven Result carries the plan digest and the
// dataset digest, and re-running the plan over the same book reproduces
// both, however the book has grown since.
func TestResultBuildPlanBindsTheResultToWhatItRan(t *testing.T) {
	b := newResultBook(t)
	p := b.profile
	bookID, last := bookIdentity(t, b)
	plan := testPlan(bookID, last)
	result, _, err := runResultBuild(t, p, resultDoc(testClaim("claim-1", "met", b.capsules[0])), "--plan", writeTestJSON(t, "plan.json", plan))
	require.NoError(t, err)
	require.NotNil(t, result.Rows)
	assert.Equal(t, 2, *result.Rows, "both published capsules are own_capsule records committed on day0")
	planDigest, err := canonical.JSONDigest(decodeJSONNumber(t, mustJSON(t, plan)))
	require.NoError(t, err)
	assert.Equal(t, planDigest, result.PlanRef)

	rowRecords := []string{recordOfCapsule(t, p, b.capsules[0]), recordOfCapsule(t, p, b.capsules[1])}
	withBook(t, p, func(book *evidencebook.Book) {
		sealed, err := book.Get(t.Context(), result.RecordID)
		require.NoError(t, err)
		statement := decodeJSONNumber(t, sealed.Header.Statement)
		assert.Equal(t, map[string]interface{}{"digest_alg": "SHA-256", "digest": result.PlanRef}, statement["plan_ref"])
		assert.Equal(t, map[string]interface{}{"digest_alg": "SHA-256", "digest": result.DatasetRef}, statement["dataset_ref"])

		// The Result cites its evidence, then the plan and the dataset.
		cites := sealed.Header.LinksTo(evidencebook.Cites)
		require.Len(t, cites, 3)
		planRecord, err := book.Get(t.Context(), cites[1])
		require.NoError(t, err)
		datasetRecord, err := book.Get(t.Context(), cites[2])
		require.NoError(t, err)
		assert.Equal(t, planRecordType, planRecord.Header.RecordType)
		assert.Equal(t, datasetRecordType, datasetRecord.Header.RecordType)
		sealedPlan, err := canonical.JSONDigest(decodeJSONNumber(t, planRecord.Header.Statement))
		require.NoError(t, err)
		assert.Equal(t, result.PlanRef, sealedPlan, "the plan record holds the bytes plan_ref names")
		sealedDataset, err := canonical.JSONDigest(decodeJSONNumber(t, datasetRecord.Header.Statement))
		require.NoError(t, err)
		assert.Equal(t, result.DatasetRef, sealedDataset, "the dataset record holds the bytes dataset_ref names")
		assert.Equal(t, append([]string{planRecord.RecordID}, rowRecords...), datasetRecord.Header.LinksTo(evidencebook.Cites), "the dataset cites its plan and every record it has a row for")
	})

	// The book grew (the plan, the dataset, the Result and a checkpoint),
	// and the same plan reproduces both digests.
	_, grown := bookIdentity(t, b)
	require.Greater(t, grown, last)
	again := planDigests(t, b, plan)
	assert.Equal(t, result.PlanRef, again.planDigest)
	assert.Equal(t, result.DatasetRef, again.datasetDigest)

	// A repeat seals nothing new.
	before := bookSize(t, p)
	repeat, _, err := runResultBuild(t, p, resultDoc(testClaim("claim-1", "met", b.capsules[0])), "--plan", writeTestJSON(t, "plan.json", plan))
	require.NoError(t, err)
	assert.True(t, repeat.AlreadyBuilt)
	assert.Equal(t, result.RecordID, repeat.RecordID)
	assert.Equal(t, before, bookSize(t, p))

	// A Result that states the digests the plan reproduces is the same
	// Result.
	stated := resultDoc(testClaim("claim-1", "met", b.capsules[0]))
	stated["plan_ref"] = map[string]any{"digest_alg": "SHA-256", "digest": result.PlanRef}
	stated["dataset_ref"] = map[string]any{"digest_alg": "SHA-256", "digest": result.DatasetRef}
	same, _, err := runResultBuild(t, p, stated, "--plan", writeTestJSON(t, "plan.json", plan))
	require.NoError(t, err)
	assert.Equal(t, result.RecordID, same.RecordID)
}

// Acceptance 2 (#107): a different plan, so a different dataset, gives a
// different Result.
func TestResultBuildADifferentPlanIsADifferentResult(t *testing.T) {
	b := newResultBook(t)
	p := b.profile
	bookID, last := bookIdentity(t, b)
	doc := resultDoc(testClaim("claim-1", "met", b.capsules[0]))
	first, _, err := runResultBuild(t, p, doc, "--plan", writeTestJSON(t, "plan.json", testPlan(bookID, last)))
	require.NoError(t, err)
	// Exclude the first capsule's record by its subject_ref: one row fewer.
	var subject string
	withBook(t, p, func(book *evidencebook.Book) {
		record, err := book.Get(t.Context(), b.capsules[0])
		require.NoError(t, err)
		subject = record.Header.SubjectRef
	})
	excluded := testPlan(bookID, last, map[string]any{"field": "subject_ref", "not_one_of": []any{subject}})
	second, _, err := runResultBuild(t, p, doc, "--plan", writeTestJSON(t, "plan.json", excluded))
	require.NoError(t, err)
	assert.NotEqual(t, first.PlanRef, second.PlanRef)
	assert.NotEqual(t, first.DatasetRef, second.DatasetRef)
	assert.Equal(t, 1, *second.Rows)
	assert.False(t, second.AlreadyBuilt)
	assert.NotEqual(t, first.RecordID, second.RecordID)
	assert.NotEqual(t, first.StatementDigest, second.StatementDigest)

	// The same rows under another plan (a filter that keeps everything) is
	// still another plan, so another Result.
	keepAll := testPlan(bookID, last, map[string]any{"field": "record_type", "equal": recordTypeOwn})
	third, _, err := runResultBuild(t, p, doc, "--plan", writeTestJSON(t, "plan.json", keepAll))
	require.NoError(t, err)
	assert.NotEqual(t, first.PlanRef, third.PlanRef)
	assert.NotEqual(t, first.RecordID, third.RecordID)
}

// The book never seals a digest it did not reproduce: a stated plan_ref or
// dataset_ref that differs from the run's is refused, and so is either
// member without --plan. Nothing is appended.
func TestResultBuildRefusesDigestsItDidNotReproduce(t *testing.T) {
	b := newResultBook(t)
	p := b.profile
	bookID, last := bookIdentity(t, b)
	plan := writeTestJSON(t, "plan.json", testPlan(bookID, last))
	other := "0000000000000000000000000000000000000000000000000000000000000000"
	run := planDigests(t, b, testPlan(bookID, last))
	for name, refs := range map[string][2]string{
		"wrong dataset_ref": {run.planDigest, other},
		"wrong plan_ref":    {other, run.datasetDigest},
	} {
		t.Run(name, func(t *testing.T) {
			doc := resultDoc(testClaim("claim-1", "met", b.capsules[0]))
			doc["plan_ref"] = map[string]any{"digest_alg": "SHA-256", "digest": refs[0]}
			doc["dataset_ref"] = map[string]any{"digest_alg": "SHA-256", "digest": refs[1]}
			before := bookSize(t, p)
			_, _, err := runResultBuild(t, p, doc, "--plan", plan)
			require.ErrorIs(t, err, cli.ErrInput)
			assert.Contains(t, cli.SafeError(err), "running --plan over this book gives digest")
			assert.Equal(t, before, bookSize(t, p))

			_, _, err = runResultBuild(t, p, doc)
			require.ErrorIs(t, err, cli.ErrInput)
			assert.Contains(t, cli.SafeError(err), "but no --plan was given")
			assert.Equal(t, before, bookSize(t, p))
		})
	}
}

// A plan the book cannot reproduce is refused before anything is sealed:
// one over another book, one reading past the last record, and one the
// Plan IR gate refuses, with its reason.
func TestResultBuildRefusesAPlanItCannotReproduce(t *testing.T) {
	b := newResultBook(t)
	p := b.profile
	bookID, last := bookIdentity(t, b)
	invalid := testPlan(bookID, last)
	invalid["nodes"].([]any)[1].(map[string]any)["params"].(map[string]any)["to"] = "2026-09-23"
	for name, tc := range map[string]struct {
		plan any
		want string
	}{
		"another book":      {testPlan("another-book", last), `--plan reads book "another-book"`},
		"past the last seq": {testPlan(bookID, last+1), "but this book's last record is seq"},
		"window reversed":   {invalid, "--plan is refused (plan-invalid)"},
		"not a plan":        {map[string]any{"nodes": []any{}}, "--plan is refused (plan-invalid)"},
		"filter field gone": {testPlan(bookID, last, map[string]any{"field": "statement.missing", "equal": "x"}), "--plan is refused (field-missing)"},
	} {
		t.Run(name, func(t *testing.T) {
			before := bookSize(t, p)
			_, _, err := runResultBuild(t, p, resultDoc(testClaim("claim-1", "met", b.capsules[0])), "--plan", writeTestJSON(t, "plan.json", tc.plan))
			require.ErrorIs(t, err, cli.ErrInput)
			assert.Contains(t, cli.SafeError(err), tc.want)
			assert.Equal(t, before, bookSize(t, p))
		})
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	require.NoError(t, err)
	return raw
}
