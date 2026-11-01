package book

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/action-state-group/agent-action-capsule/go/evidencerequest"
	"github.com/action-state-group/capsule-cli/cli"
	"github.com/action-state-group/capsule-emit/go/artifact"
	artifactjsonl "github.com/action-state-group/capsule-emit/go/artifact/jsonl"
	"github.com/action-state-group/evidencebook"
	"github.com/spf13/cobra"
)

// bookNow is the clock the book verbs use for record commit times and for
// deciding which period has ended. Tests replace it.
var bookNow = time.Now

// SourceDateEpochEnv is the reproducible-builds convention for a fixed build
// time (seconds since the Unix epoch). Set, it makes every book this process
// opens a reproduced scratch book (evidencebook#232): all record, Result and
// checkpoint times are that one time, and every record carries the header's
// reproduction marker. Unset, nothing changes.
const SourceDateEpochEnv = "SOURCE_DATE_EPOCH"

// maxEpochSeconds is 9999-12-31T23:59:59Z.
const maxEpochSeconds = 253402300799

// reproductionAt is the caller-given time of a reproduced scratch book, when
// SOURCE_DATE_EPOCH is set. An unreadable value is an input error, never the
// wall clock: a run asked to reproduce must not silently go live.
func reproductionAt() (time.Time, bool, error) {
	raw, ok := os.LookupEnv(SourceDateEpochEnv)
	if !ok {
		return time.Time{}, false, nil
	}
	seconds, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	// The year must stay four digits for the RFC 3339 times it is written as.
	if err != nil || seconds < 0 || seconds > maxEpochSeconds {
		return time.Time{}, false, cli.InputError(fmt.Sprintf("%s=%q is not a non-negative number of seconds since the Unix epoch", SourceDateEpochEnv, raw))
	}
	return time.Unix(seconds, 0).UTC(), true, nil
}

// openedBook is a book plus the record store it was opened over. The store
// is kept only so `close --capsule-out` and `result build --capsule-out` can
// export a record's sealed capsule and envelope for `verify` (exportRecord);
// nothing else reads it directly.
type openedBook struct {
	book  *evidencebook.Book
	store *evidencebook.FileStore
	// originals is the profile's artifact store, which holds every
	// published capsule and every record's originals.
	originals cli.ArtifactStore
	// lastCommit is the committed_at of the book's last record.
	lastCommit time.Time
}

// refuseDisplacedClose keeps a Close from being sealed over times that
// cannot be placed. A record dated after this clock (at most the clock
// tolerance, an hour at most, ahead of it) was committed while the clock was
// ahead; a record written since was pinned to that time. A period ending
// before it would close without the records whose real time falls in it.
// So closing (and reconciling) waits until this clock is past the book's
// last record.
func (o openedBook) refuseDisplacedClose() error {
	if _, reproduced, _ := reproductionAt(); reproduced {
		return nil // caller-timed: refuseIfAhead already holds the times in order
	}
	if now := bookNow().UTC(); o.lastCommit.After(now) {
		return cli.Hint(cli.ErrInput, fmt.Sprintf("the book's last record is committed at %s, after this clock (%s): no period can be closed or reconciled until this clock passes %s; wait until then", o.lastCommit.Format(time.RFC3339), now.Format(time.RFC3339), o.lastCommit.Format(time.RFC3339)))
	}
	return nil
}

func (o openedBook) release() error { return o.book.Release() }

// openBook is the whole signing path of the book verbs. Records are signed
// with the profile's signing key, which must be one of the profile's own
// trusted_keys (so `verify` under that profile accepts them); the book's
// checkpoints are signed with the profile's checkpoint signing key, which
// must be one of its checkpoint trusted_keys. The book's log is the
// profile's log_id. create is true only for `store init`: every other verb
// refuses a book that does not exist rather than making one.
func openBook(ctx context.Context, p cli.Profile, create bool) (openedBook, error) {
	return openBookWith(ctx, p, create, nil)
}

// openBookWith is openBook over an artifact store the caller already opened
// (nil opens the profile's own).
func openBookWith(ctx context.Context, p cli.Profile, create bool, artifacts cli.ArtifactStore) (openedBook, error) {
	opened, err := openBookUnguarded(ctx, p, create, artifacts)
	if err != nil {
		return opened, err
	}
	if err = requireNoRetiredEntries(ctx, p, opened.book); err != nil {
		return openedBook{}, errors.Join(err, opened.release())
	}
	return opened, nil
}

// openBookUnguarded is openBook without the retired-log check; only
// `book migrate`, which is what satisfies that check, calls it directly.
func openBookUnguarded(ctx context.Context, p cli.Profile, create bool, artifacts cli.ArtifactStore) (openedBook, error) {
	if !isBook(p) {
		return openedBook{}, cli.InputError("book verbs need a jsonl profile with log_id and operator set, and `log: book` (the log is not this plugin's otherwise)")
	}
	if p.OperatorMissing() {
		// A profile saved by an earlier capsulectl, which did not require one.
		return openedBook{}, cli.InputError(fmt.Sprintf("profile %s has no operator, and the operator names who signs every record the book seals: set it with `capsulectl profile update --profile %s --operator <name>`", p.Name, p.Name))
	}
	recordKey, err := cli.PrivateKey(p.Signing)
	if err != nil {
		return openedBook{}, err
	}
	if err = cli.RequirePublisherKey(p, recordKey); err != nil {
		return openedBook{}, err
	}
	checkpointKey, err := cli.PrivateKey(p.Checkpoint.Signing)
	if err != nil {
		return openedBook{}, err
	}
	if !cli.CheckpointSignerTrusted(p, hex.EncodeToString(checkpointKey.Public().(ed25519.PublicKey))) {
		return openedBook{}, cli.InputError("the checkpoint signing key is not in checkpoint.trusted_keys: add its public key with `capsulectl profile update --profile " + p.Name + " --checkpoint-trusted-key <64-hex key>`")
	}
	signer, err := evidencebook.NewEd25519Signer(recordKey)
	if err != nil {
		return openedBook{}, err
	}
	// The log is opened first: it takes an exclusive lock that a second
	// process cannot, and the record store repairs a torn final line when it
	// opens, which must never happen under another process's write.
	dir := filepath.Join(p.Connection.Database, "book")
	if !create {
		if _, err = os.Stat(filepath.Join(dir, "log.jsonl")); err != nil {
			return openedBook{}, noBookError(p)
		}
	}
	if err = os.MkdirAll(dir, 0o700); err != nil {
		return openedBook{}, err
	}
	substrate, err := evidencebook.OpenCLL(filepath.Join(dir, "log.jsonl"), p.LogID, checkpointKey)
	if err != nil {
		return openedBook{}, err
	}
	store, err := evidencebook.OpenFileStore(filepath.Join(dir, "records"))
	if err != nil {
		return openedBook{}, errors.Join(err, substrate.Release())
	}
	if artifacts == nil {
		if artifacts, err = bookOriginals(ctx, p); err != nil {
			return openedBook{}, errors.Join(err, store.Release(), substrate.Release())
		}
	} else if artifacts, err = durableOriginals(p, artifacts); err != nil {
		return openedBook{}, errors.Join(err, store.Release(), substrate.Release())
	}
	// Under the log's lock, before the book reads a payload: the payload
	// directory an earlier capsulectl kept moves into the artifact store.
	if err = migratePayloadDir(ctx, store, artifacts, filepath.Join(dir, "payloads"), filepath.Join(p.Connection.Database, cli.JSONLArtifactFile)); err != nil {
		return openedBook{}, errors.Join(err, store.Release(), substrate.Release())
	}
	clock := &monotonicClock{}
	at, reproduced, err := reproductionAt()
	if err != nil {
		return openedBook{}, errors.Join(err, store.Release(), substrate.Release())
	}
	if reproduced {
		clock.fixed = &at
	}
	book, err := evidencebook.Open(ctx, evidencebook.Config{
		BookID: p.LogID, Operator: p.Operator,
		Store: store, Substrate: substrate, Originals: artifacts, Signer: signer,
		Now: clock.now, Reproduction: reproduced,
	})
	if err != nil {
		return openedBook{}, errors.Join(bookError(err), store.Release(), substrate.Release())
	}
	if err = clock.floorAtLastRecord(ctx, book); err != nil {
		return openedBook{}, errors.Join(err, book.Release())
	}
	if err = clock.refuseIfAhead(p); err != nil {
		return openedBook{}, errors.Join(err, book.Release())
	}
	// A book an earlier capsulectl checkpointed under another log id (the
	// "<log_id>/book" it once used) would fail at its next checkpoint; say so
	// now instead of letting commands run until then.
	checkpoints, err := book.Checkpoints(ctx)
	if err == nil && len(checkpoints) > 0 && checkpoints[len(checkpoints)-1].LogID != p.LogID {
		err = cli.Hint(cli.ErrInput, fmt.Sprintf("this book is checkpointed under log id %q, not the profile's log_id: an earlier capsulectl made it; move the book/ directory aside, then run 'store init' (or 'book migrate' if cll.jsonl holds entries)", checkpoints[len(checkpoints)-1].LogID))
	}
	if err != nil {
		return openedBook{}, errors.Join(err, book.Release())
	}
	return openedBook{book: book, store: store, originals: artifacts, lastCommit: clock.floor}, nil
}

// bookOriginals opens the artifact store a book profile keeps its originals
// in: the profile's own artifacts.jsonl, under its namespace and trusted
// keys (the book's signing key among them). A profile without a namespace
// has none, and a record that needs one is refused.
func bookOriginals(ctx context.Context, p cli.Profile) (cli.ArtifactStore, error) {
	if p.Namespace == "" {
		return noOriginals{profile: p.Name}, nil
	}
	keys, err := cli.ParseKeys(p.TrustedKeys)
	if err != nil {
		return nil, err
	}
	if len(keys) == 0 {
		return nil, cli.InputError("artifact operations require producer trusted_keys")
	}
	store, err := artifactjsonl.New(filepath.Join(p.Connection.Database, cli.JSONLArtifactFile), p.Namespace, keys)
	if err != nil {
		return nil, err
	}
	if err = store.Init(ctx); err != nil {
		return nil, err
	}
	return durableOriginals(p, store)
}

// durableOriginals is a book profile's artifact store as its book needs it:
// every Put and Purge synced before it returns (the book stores a record
// only once its originals are durable), and the file owner-only, since it
// holds the book's originals, which book/payloads kept private.
func durableOriginals(p cli.Profile, store cli.ArtifactStore) (cli.ArtifactStore, error) {
	if _, isRefusing := store.(noOriginals); isRefusing {
		return store, nil
	}
	path := filepath.Join(p.Connection.Database, cli.JSONLArtifactFile)
	if err := os.Chmod(path, 0o600); err != nil {
		return nil, err
	}
	return durable{ArtifactStore: store, synced: evidencebook.SyncedFile(store, path)}, nil
}

// durable is a cli.ArtifactStore whose writes go through the synced store.
type durable struct {
	cli.ArtifactStore
	synced evidencebook.ArtifactStore
}

func (d durable) Put(ctx context.Context, record artifact.Record) error {
	return d.synced.Put(ctx, record)
}
func (d durable) Purge(ctx context.Context, id string) error { return d.synced.Purge(ctx, id) }

// noOriginals is the artifact store of a book profile with no namespace: it
// holds nothing and refuses to hold anything.
type noOriginals struct{ profile string }

func (n noOriginals) refuse() error {
	return cli.InputError("profile " + n.profile + " has no namespace, so its book has no artifact store for originals: `capsulectl profile update --profile " + n.profile + " --namespace NAME`")
}

func (n noOriginals) Init(context.Context) error                 { return nil }
func (n noOriginals) Put(context.Context, artifact.Record) error { return n.refuse() }
func (n noOriginals) Get(context.Context, string) (artifact.Record, error) {
	return artifact.Record{}, errors.Join(artifact.ErrNotFound, n.refuse())
}
func (n noOriginals) Purge(context.Context, string) error { return n.refuse() }

// migratePayloadDir moves the book/payloads directory a book profile kept
// before capsule-cli#236 into the artifact store (the file at artifactsPath),
// once, and removes it. No record, log entry or checkpoint changes. The
// artifact store's file is synced before the directory goes, and a failure
// leaves the directory in place for the next open to resume.
func migratePayloadDir(ctx context.Context, store *evidencebook.FileStore, originals cli.ArtifactStore, dir, artifactsPath string) error {
	if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil {
		return err
	}
	if _, err := evidencebook.MigratePayloadDir(ctx, store, originals, dir); err != nil {
		return errors.Join(cli.Hint(cli.ErrConflict, "the book's payloads could not be moved into the profile's artifact store; book/payloads is kept and the move is retried at the next open"), err)
	}
	// Synced on every run, a resumed one that moved nothing included: an
	// earlier run may have stored the payloads and stopped before its sync.
	if err := syncFile(artifactsPath); err != nil {
		return err
	}
	return os.RemoveAll(dir)
}

// syncFile flushes a file's contents to stable storage.
func syncFile(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR, 0)
	if err != nil {
		return err
	}
	return errors.Join(f.Sync(), f.Close())
}

// monotonicClock is the book's commit clock: bookNow, never earlier than the
// last record's committed_at. A wall clock that steps back (NTP, a resumed
// VM, a store shared between machines) would otherwise commit a record
// "before" its predecessor, and no period containing it could ever be mapped
// onto log positions again. Held at the floor, the time still says when the
// record was committed at the latest.
//
// The floor has an upper bound: a book whose last record is dated more than
// the profile's clock tolerance (at most an hour) ahead of this clock is
// refused for writing. Records written while the last record is ahead of the
// clock by less than that are pinned to its time, so a record appended up to
// an hour before a period ends can be committed after it: a Close counts it
// in the next period, and a period left without an exchange by such pins is
// refused (refuseExchangelessWindowWithPinnedTail). Every verb that signs,
// Close included, opens the book this way.
type monotonicClock struct {
	floor time.Time
	// fixed is a reproduced scratch book's caller-given time. It is every
	// stamp's time, and never an earlier one than the book's last record.
	fixed *time.Time
}

func (c *monotonicClock) now() time.Time {
	if c.fixed != nil {
		return *c.fixed
	}
	t := bookNow().UTC()
	if t.Before(c.floor) {
		t = c.floor
	}
	c.floor = t
	return t
}

func (c *monotonicClock) refuseIfAhead(p cli.Profile) error {
	if c.fixed != nil {
		// Caller times are monotonic with seq: one earlier than the last
		// record's is refused, not pinned up to it. The wall clock plays no
		// part, so there is no tolerance to measure.
		if c.fixed.Before(c.floor) {
			return cli.Hint(cli.ErrInput, fmt.Sprintf("%s is %s, earlier than the book's last record (%s): a reproduced book's times only move forward", SourceDateEpochEnv, c.fixed.Format(time.RFC3339), c.floor.Format(time.RFC3339)))
		}
		return nil
	}
	tolerance, err := p.ClockToleranceDuration()
	if err != nil {
		return err
	}
	if now := bookNow().UTC(); c.floor.Sub(now) > tolerance {
		return cli.Hint(cli.ErrInput, fmt.Sprintf("the book's last record is committed at %s, %s ahead of this clock (tolerance %s). If this clock is wrong, fix it. If the clock jumped forward and back: wait until this clock passes %s if that is hours away; otherwise start a new log -- move the profile's book/ directory aside (keep it), then run 'capsulectl profile update --profile %s --log-id <new-log-id>' and 'capsulectl store init --profile %s'", c.floor.Format(time.RFC3339), c.floor.Sub(now).Round(time.Second), tolerance, c.floor.Format(time.RFC3339), p.Name, p.Name))
	}
	return nil
}

func (c *monotonicClock) floorAtLastRecord(ctx context.Context, book *evidencebook.Book) error {
	size := book.Size()
	if size == 0 {
		return nil
	}
	last, err := book.Query(ctx, evidencebook.Filter{FromSeq: size, ToSeq: size})
	if err != nil || len(last) == 0 {
		return err
	}
	at, err := committedAt(last[0].Header)
	if err != nil {
		return err
	}
	if at.After(c.floor) {
		c.floor = at
	}
	return nil
}

// bookError keeps the book's own input classes on exit code 2.
func bookError(err error) error {
	// The book's own messages name records and fields, never payloads.
	if errors.Is(err, evidencebook.ErrNotFound) {
		return errors.Join(cli.InputError("not found in the profile's evidence book: "+err.Error()), err)
	}
	if errors.Is(err, evidencebook.ErrInvalid) {
		return errors.Join(cli.InputError("the evidence book refused this as invalid: "+err.Error()), err)
	}
	return err
}

// -- periods and windows ---------------------------------------------------

// period is one UTC day, one ISO week (Monday start) or one UTC calendar
// month. A Close is idempotent on its bounds (on its key, for a Close sealed
// before close/v1).
type period struct {
	key        string
	start, end time.Time
}

// parsePeriod resolves --period/--date. With no --date it takes the most
// recent period that has ended. A period that has not ended is refused:
// closing it would freeze a window that is still receiving records.
func parsePeriod(kind, date string, now time.Time) (period, error) {
	now = now.UTC()
	var day time.Time
	if date == "" {
		switch kind {
		case "day":
			day = now.AddDate(0, 0, -1)
		case "week":
			day = now.AddDate(0, 0, -7)
		case "month":
			// The first of last month: AddDate(0, -1, 0) on the 31st can
			// land in this month.
			day = time.Date(now.Year(), now.Month()-1, 1, 0, 0, 0, 0, time.UTC)
		}
	} else {
		parsed, err := time.Parse(time.DateOnly, date)
		if err != nil {
			return period{}, cli.InputError("--date must be YYYY-MM-DD")
		}
		day = parsed
	}
	day = time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, time.UTC)
	var p period
	switch kind {
	case "day":
		p = period{key: "day:" + day.Format(time.DateOnly), start: day, end: day.AddDate(0, 0, 1)}
	case "week":
		start := day.AddDate(0, 0, -((int(day.Weekday()) + 6) % 7))
		year, week := start.ISOWeek()
		p = period{key: fmt.Sprintf("week:%04d-W%02d", year, week), start: start, end: start.AddDate(0, 0, 7)}
	case "month":
		start := time.Date(day.Year(), day.Month(), 1, 0, 0, 0, 0, time.UTC)
		p = period{key: "month:" + start.Format("2006-01"), start: start, end: start.AddDate(0, 1, 0)}
	default:
		return period{}, cli.InputError("--period must be day, week or month")
	}
	if p.end.After(now) {
		return period{}, cli.InputError("the period has not ended yet")
	}
	return p, nil
}

// placed is one record whose commit time is known from a verified header.
type placed struct {
	seq uint64
	at  time.Time
}

// seqWindow maps a period onto one log's positions. records must be in log
// order. from is one past the last placed record committed before the
// period starts; to is one before the first placed record committed at or
// after it ends. A record that cannot be placed (a withheld or unverified
// header) between those bounds is therefore inside the window, where it
// makes the account incomplete instead of silently falling out. from > to
// is an empty window.
//
// ended reports whether a placed record shows the log continued past the
// period's end; when none does, to is last and the caller decides what an
// unproven end means. Commit times that go backwards in log order are
// refused: no position then separates the period from its neighbours.
// to == 0 would read as an open end to the log, so it is refused too.
func seqWindow(records []placed, p period, last uint64) (from, to uint64, ended bool, err error) {
	from, to = 1, last
	var prev placed
	for _, r := range records {
		if r.seq <= prev.seq || r.at.Before(prev.at) {
			return 0, 0, false, cli.InputError(fmt.Sprintf("commit times go backwards at position %d; the period cannot be placed in this log", r.seq))
		}
		prev = r
		if r.at.Before(p.start) {
			from = r.seq + 1
		} else if !r.at.Before(p.end) && !ended {
			to, ended = r.seq-1, true
		}
	}
	if to == 0 {
		return 0, 0, false, cli.Hint(cli.ErrInput, "the log holds nothing committed before the period ends; there is nothing to close for it")
	}
	return from, to, ended, nil
}

func committedAt(h evidencebook.Header) (time.Time, error) {
	at, err := time.Parse(time.RFC3339Nano, h.CommittedAt)
	if err != nil {
		return time.Time{}, fmt.Errorf("record %d: committed_at: %w", h.Seq, err)
	}
	return at, nil
}

func ownWindow(ctx context.Context, book *evidencebook.Book, p period) (from, to uint64, err error) {
	records, err := book.Query(ctx, evidencebook.Filter{})
	if err != nil {
		return 0, 0, err
	}
	all := make([]placed, 0, len(records))
	for _, r := range records {
		at, err := committedAt(r.Header)
		if err != nil {
			return 0, 0, err
		}
		all = append(all, placed{seq: r.Seq, at: at})
	}
	from, to, _, err = seqWindow(all, p, book.Size())
	return from, to, err
}

// peerWindow maps the period onto the peer's positions. When no record the
// peer disclosed shows its log continuing past the period's end, the bundle
// may have been cut mid-period: the window then runs one position past the
// bundle's interval, which the book reads as an incomplete account.
func peerWindow(peer evidencebook.VerifiedBundle, p period) (from, to uint64, err error) {
	var known []placed
	for _, r := range peer.Records {
		if r.Header == nil || !r.HeaderVerified || !r.CapsuleOK {
			continue
		}
		at, err := committedAt(*r.Header)
		if err != nil {
			return 0, 0, errors.Join(cli.InputError(fmt.Sprintf("the peer bundle's record at position %d has a committed_at that is not an RFC 3339 time", r.Seq)), err)
		}
		known = append(known, placed{seq: r.Seq, at: at})
	}
	from, to, ended, err := seqWindow(known, p, peer.IntervalLast)
	if err != nil {
		return 0, 0, err
	}
	if !ended {
		to = peer.IntervalLast + 1
	}
	return from, to, nil
}

// readPeer verifies a held peer bundle and pins its checkpoint key. The
// bundle verifier authenticates the checkpoint under whatever key the bundle
// names; only the caller knows which key is the peer's, so an unpinned or
// mismatched key is refused rather than reconciled against.
//
// The bundle must also be the counterparty's: every record whose header
// verified names --counterparty as its book, and at least one does.
func readPeer(path, key, counterparty string) (evidencebook.VerifiedBundle, error) {
	pinned, err := cli.ParseKeys([]string{key})
	if err != nil || key == "" {
		return evidencebook.VerifiedBundle{}, cli.InputError("--peer needs --peer-checkpoint-key, a 32-byte Ed25519 public key in hex")
	}
	raw, err := cli.ReadInput(path)
	if err != nil {
		return evidencebook.VerifiedBundle{}, err
	}
	peer, err := evidencebook.VerifyBundle(raw)
	if err != nil {
		return evidencebook.VerifiedBundle{}, errors.Join(cli.InputError("the --peer bundle does not verify: "+err.Error()), err)
	}
	if !peer.AnchorAuthenticated || peer.AnchorKeyID != hex.EncodeToString(pinned[0]) {
		return evidencebook.VerifiedBundle{}, cli.InputError("the peer bundle's checkpoint is not signed by --peer-checkpoint-key")
	}
	named := false
	for _, r := range peer.Records {
		if r.Header == nil || !r.HeaderVerified {
			continue
		}
		if r.Header.BookID != counterparty {
			return evidencebook.VerifiedBundle{}, cli.InputError("the peer bundle holds records of a book other than --counterparty")
		}
		named = true
	}
	if !named {
		return evidencebook.VerifiedBundle{}, cli.InputError("the peer bundle discloses no header naming its book")
	}
	return peer, nil
}

// -- close and reconcile ---------------------------------------------------

const closeComparator = "same_payload_commitments"

// closeProfile is the profile string a Close sealed before close/v1 stated:
// this verb's version, the period, and the comparator. A legacy Close with
// this profile is this period's Close.
func closeProfile(p period) string {
	return "capsulectl-close/v1 " + p.key + " comparator=" + closeComparator
}

type reconcileResult struct {
	Period         string                      `json:"period"`
	Comparator     string                      `json:"comparator"`
	Reconciliation evidencebook.Reconciliation `json:"reconciliation"`
}

// closeResult is what `close` prints. Reconciliation is the reconciliation
// a fresh Close ran (pairs and windows); a repeat prints the one a legacy
// Close sealed, and none for a close/v1 Close, whose body has no pairs.
// Close is the sealed body as stored.
type closeResult struct {
	RecordID       string                       `json:"record_id"`
	Seq            uint64                       `json:"seq"`
	Period         string                       `json:"period"`
	Counterparty   string                       `json:"counterparty"`
	AlreadyClosed  bool                         `json:"already_closed"`
	PeerBundle     string                       `json:"peer_bundle,omitempty"`
	PeerClose      string                       `json:"peer_close,omitempty"`
	Reconciliation *evidencebook.Reconciliation `json:"reconciliation,omitempty"`
	Close          json.RawMessage              `json:"close"`
	Capsule        string                       `json:"capsule,omitempty"`
	Bundle         string                       `json:"bundle,omitempty"`
	BundleDigest   string                       `json:"bundle_digest,omitempty"`
}

// sealedClose is one Close read by its record_version: exactly one of v1
// and legacy is set.
type sealedClose struct {
	record evidencebook.Record
	v1     *evidencebook.CloseStatement
	legacy *evidencebook.LegacyCloseStatement
}

func readSealedClose(record evidencebook.Record) (sealedClose, error) {
	v1, legacy, err := evidencebook.ReadClose(record.Header)
	if err != nil {
		return sealedClose{}, fmt.Errorf("close %s: %w", record.RecordID, err)
	}
	return sealedClose{record: record, v1: v1, legacy: legacy}, nil
}

// closes reports whether c is this book's Close for p: a close/v1 Close
// states p's bounds, a legacy one closeProfile(p).
func (c sealedClose) closes(p period) bool {
	if c.v1 != nil {
		return c.v1.Period.Start.Equal(p.start) && c.v1.Period.End.Equal(p.end)
	}
	return c.legacy.Profile == closeProfile(p)
}

// window returns the own positions c binds: a legacy Close sealed them; a
// close/v1 Close's are recovered from its head and counts.
func (c sealedClose) window(ctx context.Context, book *evidencebook.Book) (from, to uint64, err error) {
	if c.legacy != nil {
		return c.legacy.Reconciliation.FromSeq, c.legacy.Reconciliation.ToSeq, nil
	}
	head, err := book.Get(ctx, c.v1.Head.Digest)
	if err != nil {
		return 0, 0, fmt.Errorf("close %s: head: %w", c.record.RecordID, err)
	}
	return c.v1.Window(head.Seq, c.record.Seq)
}

// priorCloses returns this book's Closes for one counterparty, in log order.
func priorCloses(ctx context.Context, book *evidencebook.Book, counterparty string) ([]sealedClose, error) {
	records, err := book.Query(ctx, evidencebook.Filter{RecordType: evidencebook.RecordTypeClose, CounterpartyRef: counterparty})
	if err != nil {
		return nil, err
	}
	closes := make([]sealedClose, len(records))
	for i, r := range records {
		if closes[i], err = readSealedClose(r); err != nil {
			return nil, err
		}
	}
	return closes, nil
}

// reconcileInput builds the windows both verbs share; peerWindowFromPeriod
// places the peer's window by the period (with no peer Close to take it
// from). A zero peer (no --peer) is an account that proves nothing, so every own exchange in the
// window reads INSUFFICIENT: one half unavailable is never disagreement.
//
// The book reconciles every correlated record in the window; it cannot
// select one counterparty's. So a window holding an exchange that names a
// different counterparty is refused: counting it would report A_ONLY to a
// party that never saw it and link the Close to it. An exchange that names
// no counterparty is counted in every Close.
func reconcileInput(ctx context.Context, book *evidencebook.Book, p period, counterparty string, peer evidencebook.VerifiedBundle, peerWindowFromPeriod bool) (evidencebook.ReconcileInput, error) {
	from, to, err := ownWindow(ctx, book, p)
	if err != nil {
		return evidencebook.ReconcileInput{}, err
	}
	if from <= to {
		window, err := book.Query(ctx, evidencebook.Filter{FromSeq: from, ToSeq: to})
		if err != nil {
			return evidencebook.ReconcileInput{}, err
		}
		for _, r := range window {
			if r.Header.Correlation != nil && r.Header.CounterpartyRef != "" && r.Header.CounterpartyRef != counterparty {
				return evidencebook.ReconcileInput{}, cli.InputError(fmt.Sprintf("the window holds an exchange with another counterparty (position %d); a Close cannot yet be limited to one counterparty's exchanges", r.Seq))
			}
		}
	}
	in := evidencebook.ReconcileInput{Peer: peer, FromSeq: from, ToSeq: to, Compare: evidencebook.SamePayloadCommitments}
	if peerWindowFromPeriod {
		if in.PeerFromSeq, in.PeerToSeq, err = peerWindow(peer, p); err != nil {
			return evidencebook.ReconcileInput{}, err
		}
	}
	return in, nil
}

func periodFlags(c *cobra.Command) {
	c.Flags().String("period", "", "day, week or month (UTC; weeks start Monday)")
	c.Flags().String("date", "", "Any date inside the period, YYYY-MM-DD; default: the most recent period that has ended")
	c.Flags().String("counterparty", "", "The counterparty's book id; a peer bundle must be from that book")
	c.Flags().String("peer", "", "A held Evidence Bundle from the counterparty's book (never fetched)")
	c.Flags().String("peer-checkpoint-key", "", "The counterparty's checkpoint public key in hex, obtained independently")
}

type periodArgs struct {
	period       period
	counterparty string
	peer         evidencebook.VerifiedBundle
	hasPeer      bool
}

func periodAndPeer(c *cobra.Command) (periodArgs, error) {
	var a periodArgs
	a.counterparty, _ = c.Flags().GetString("counterparty")
	if a.counterparty == "" {
		return a, cli.InputError("--counterparty is required: the counterparty's book id")
	}
	kind, _ := c.Flags().GetString("period")
	date, _ := c.Flags().GetString("date")
	var err error
	if a.period, err = parsePeriod(kind, date, clockNow()); err != nil {
		return a, err
	}
	path, _ := c.Flags().GetString("peer")
	key, _ := c.Flags().GetString("peer-checkpoint-key")
	if path == "" {
		if key != "" {
			return a, cli.InputError("--peer-checkpoint-key given without --peer")
		}
		return a, nil
	}
	a.peer, err = readPeer(path, key, a.counterparty)
	a.hasPeer = err == nil
	return a, err
}

func reconcileCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "reconcile", Short: "Reconcile a period against a held peer bundle as of this book's latest checkpoint; adds no record", Args: cli.NoArgs, RunE: func(c *cobra.Command, _ []string) (err error) {
		p, e := cli.Selected(c)
		if e != nil {
			return e
		}
		a, e := periodAndPeer(c)
		if e != nil {
			return e
		}
		if !a.hasPeer {
			return cli.InputError("--peer is required: a held Evidence Bundle file from the counterparty's book")
		}
		opened, e := openBook(c.Context(), p, false)
		if e != nil {
			return e
		}
		defer func() { err = errors.Join(err, opened.release()) }()
		if e = opened.refuseDisplacedClose(); e != nil {
			return e
		}
		in, e := reconcileInput(c.Context(), opened.book, a.period, a.counterparty, a.peer, true)
		if e != nil {
			return e
		}
		recon, e := opened.book.Reconcile(c.Context(), in)
		if e != nil {
			return bookError(e)
		}
		return cli.Output(c, reconcileResult{Period: a.period.key, Comparator: closeComparator, Reconciliation: recon})
	}}
	periodFlags(cmd)
	return cmd
}

// refuseExchangelessWindowWithPinnedTail keeps a period from closing as all
// zeros when its exchanges were pinned out of it. While the book's last
// record was ahead of the clock, each record written was committed at that
// same time: its committed_at equals its predecessor's, to the microsecond,
// which two appends made at their own time do not produce. A pinned record
// committed within the clock ceiling after the period's end may have been
// appended during the period. When the window holds no exchange -- it is
// empty, or holds only the book's own records such as checkpoint index roots
// and earlier Closes -- that is the whole period's account, so the Close is
// refused. A window with exchanges closes, and a pinned record near its end
// may be counted in the next period (see the README).
func refuseExchangelessWindowWithPinnedTail(ctx context.Context, book *evidencebook.Book, p period, in evidencebook.ReconcileInput) error {
	if in.FromSeq <= in.ToSeq {
		window, err := book.Query(ctx, evidencebook.Filter{FromSeq: in.FromSeq, ToSeq: in.ToSeq})
		if err != nil {
			return err
		}
		for _, r := range window {
			if r.Header.Correlation != nil {
				return nil
			}
		}
	}
	records, err := book.Query(ctx, evidencebook.Filter{FromSeq: in.ToSeq})
	if err != nil {
		return err
	}
	var previous time.Time
	pinned := 0
	for i, r := range records {
		at, err := committedAt(r.Header)
		if err != nil {
			return err
		}
		if i > 0 {
			if at.Sub(p.end) > cli.MaxClockTolerance {
				break
			}
			if at.Equal(previous) {
				pinned++
			}
		}
		previous = at
	}
	if pinned > 0 {
		return cli.Hint(cli.ErrInput, fmt.Sprintf("%d record(s) committed within an hour after this period ends were pinned there while the clock was behind the book (clock skew): they may have been appended during the period, so it is not closed without exchanges. Waiting does not change that. Close the next period with --since-last instead (its window starts after the previous Close, so it covers these records), or start a new log to close periods exactly from here on", pinned))
	}
	return nil
}

// sinceLastFrom moves a window's start back to just after the furthest
// position any earlier Close with this counterparty reached, so an unclosed
// day between two Closes is not skipped. A previous Close that already
// reached into this window (a period closed out of order) is refused.
func sinceLastFrom(ctx context.Context, book *evidencebook.Book, closes []sealedClose, from uint64) (uint64, error) {
	var reached uint64
	for _, c := range closes {
		_, to, err := c.window(ctx, book)
		if err != nil {
			return 0, err
		}
		reached = max(reached, to)
	}
	if reached >= from {
		return 0, cli.InputError("an earlier Close with this counterparty already reaches into this period; --since-last needs periods closed in order")
	}
	return reached + 1, nil
}

// peerCloseCandidate is a Close the peer bundle discloses for this period.
type peerCloseCandidate struct {
	record evidencebook.PeerRecord
	close  sealedClose
}

// citablePeerClose picks the counterparty's Close for this period that a
// Close cites, and the peer positions that Close binds, which the
// reconciliation then runs over so its tallies answer the Close it cites.
// Only records the bundle verified and its authenticated checkpoint covers
// count. A candidate is a Close from the counterparty's book naming this
// book as its counterparty, for this period (close/v1 bounds, or a legacy
// profile whose period closeProfilePeriod derives). A candidate another
// candidate supersedes is dropped. None: nil, and the Close states counts
// only. More than one: refused, since the bundle does not say which Close
// the tallies would answer.
func citablePeerClose(peer evidencebook.VerifiedBundle, ownBook string, p period) (*peerCloseCandidate, uint64, uint64, error) {
	var candidates []peerCloseCandidate
	superseded := map[string]bool{}
	for _, r := range peer.Records {
		if r.Header == nil || !r.HeaderVerified || !r.CapsuleOK || !peer.Covers(r.Seq) || r.Header.RecordType != evidencebook.RecordTypeClose || r.Header.CounterpartyRef != ownBook {
			continue
		}
		c, err := readSealedClose(evidencebook.Record{RecordID: r.RecordID, Seq: r.Seq, Header: *r.Header})
		if err != nil {
			return nil, 0, 0, errors.Join(cli.InputError(fmt.Sprintf("the peer bundle's Close at position %d does not read as a Close", r.Seq)), err)
		}
		if !c.closes(p) {
			if c.legacy == nil {
				continue
			}
			start, end, ok := closeProfilePeriod(c.legacy.Profile)
			if !ok || !start.Equal(p.start) || !end.Equal(p.end) {
				continue
			}
		}
		candidates = append(candidates, peerCloseCandidate{record: r, close: c})
		for _, target := range r.Header.LinksTo(evidencebook.Supersedes) {
			superseded[target] = true
		}
	}
	var current []peerCloseCandidate
	for _, c := range candidates {
		if !superseded[c.record.RecordID] {
			current = append(current, c)
		}
	}
	switch len(current) {
	case 0:
		return nil, 0, 0, nil
	case 1:
	default:
		ids := make([]string, len(current))
		for i, c := range current {
			ids[i] = c.record.RecordID
		}
		return nil, 0, 0, cli.InputError(fmt.Sprintf("the peer bundle discloses %d Closes for this period that none supersedes (%s); a Close cites exactly one", len(current), strings.Join(ids, ", ")))
	}
	cited := current[0]
	if cited.close.legacy != nil {
		return &cited, cited.close.legacy.Reconciliation.FromSeq, cited.close.legacy.Reconciliation.ToSeq, nil
	}
	for _, r := range peer.Records {
		if r.RecordID == cited.close.v1.Head.Digest {
			from, to, err := cited.close.v1.Window(r.Seq, cited.record.Seq)
			if err != nil {
				return nil, 0, 0, errors.Join(cli.InputError(fmt.Sprintf("the peer's Close %s states a window its head cannot hold", cited.record.RecordID)), err)
			}
			return &cited, from, to, nil
		}
	}
	return nil, 0, 0, cli.InputError(fmt.Sprintf("the peer bundle does not disclose the head of the peer's Close %s, so the positions it binds cannot be placed", cited.record.RecordID))
}

func closeCommand() *cobra.Command {
	cmd := &cobra.Command{Use: "close", Short: "Seal the book's Close for one ended period and counterparty (signs; a repeat seals no second Close)", Args: cli.NoArgs, RunE: func(c *cobra.Command, _ []string) (err error) {
		p, e := cli.Selected(c)
		if e != nil {
			return e
		}
		a, e := periodAndPeer(c)
		if e != nil {
			return e
		}
		sinceLast, _ := c.Flags().GetBool("since-last")
		if sinceLast && a.hasPeer {
			return cli.InputError("--since-last cannot be combined with --peer: the peer's window cannot be stretched to match")
		}
		opened, e := openBook(c.Context(), p, false)
		if e != nil {
			return e
		}
		defer func() { err = errors.Join(err, opened.release()) }()
		if e = opened.refuseDisplacedClose(); e != nil {
			return e
		}
		book := opened.book
		result := closeResult{Period: a.period.key, Counterparty: a.counterparty}
		closes, e := priorCloses(c.Context(), book, a.counterparty)
		if e != nil {
			return e
		}
		var sealed evidencebook.Record
		var from, to uint64
		for _, prior := range closes {
			if prior.closes(a.period) {
				sealed, result.AlreadyClosed = prior.record, true
				if prior.legacy != nil {
					result.Reconciliation = &prior.legacy.Reconciliation
				}
				if from, to, e = prior.window(c.Context(), book); e != nil {
					return e
				}
				break
			}
		}
		if !result.AlreadyClosed {
			// The window is fixed, and refused if it must be, before anything
			// is written. Then a checkpoint covers every own record in it, so
			// the book's own account of the period is complete.
			// A cited peer Close fixes the peer window, so it is chosen before
			// any window is derived from the period: the period's placement
			// in the peer's log is then never consulted.
			closeIn := evidencebook.CloseInput{Counterparty: a.counterparty, Period: evidencebook.ClosePeriod{Start: a.period.start, End: a.period.end}}
			var cited *peerCloseCandidate
			var peerFrom, peerTo uint64
			if a.hasPeer {
				ownBook, _ := book.Info()
				if cited, peerFrom, peerTo, e = citablePeerClose(a.peer, ownBook, a.period); e != nil {
					return e
				}
			}
			in, e := reconcileInput(c.Context(), book, a.period, a.counterparty, a.peer, a.hasPeer && cited == nil)
			if e != nil {
				return e
			}
			if cited != nil {
				closeIn.PeerClose, in.PeerFromSeq, in.PeerToSeq = cited.record.RecordID, peerFrom, peerTo
			}
			if sinceLast && len(closes) > 0 {
				if in.FromSeq, e = sinceLastFrom(c.Context(), book, closes, in.FromSeq); e != nil {
					return e
				}
			}
			if e = refuseExchangelessWindowWithPinnedTail(c.Context(), book, a.period, in); e != nil {
				return e
			}
			if _, e = book.Checkpoint(c.Context()); e != nil {
				return e
			}
			// The reconciliation Close runs, printed with its pairs: the body
			// carries only its tallies, and only when it cites a peer Close.
			recon, e := book.Reconcile(c.Context(), in)
			if e != nil {
				return bookError(e)
			}
			closeIn.Reconcile = in
			if sealed, e = book.Close(c.Context(), closeIn); e != nil {
				return bookError(e)
			}
			result.Reconciliation, from, to = &recon, in.FromSeq, in.ToSeq
			result.PeerClose = closeIn.PeerClose
			if a.hasPeer {
				result.PeerBundle = a.peer.Digest
			}
		}
		result.RecordID, result.Seq, result.Close = sealed.RecordID, sealed.Seq, sealed.Header.Statement
		// The Close is sealed from here on: a failure writing either file
		// still reports its record_id, so the caller never loses it.
		if e = closeOutputs(c, opened, p, &result, sealed, from, to); e != nil {
			return errors.Join(e, cli.Output(c, result))
		}
		return cli.Output(c, result)
	}}
	periodFlags(cmd)
	cmd.Flags().Bool("since-last", false, "Start the window just after the furthest position an earlier Close with this counterparty reached")
	cmd.Flags().String("capsule-out", "", "Also write the Close as an artifact.Record for `verify --capsule`; a file already holding exactly those bytes is left alone (a repeat is a no-op), any other file there is refused")
	cmd.Flags().String("bundle-out", "", "Also write an Evidence Bundle rooted at the Close (commits a disclosure record and a checkpoint)")
	return cmd
}

func closeOutputs(c *cobra.Command, opened openedBook, p cli.Profile, result *closeResult, sealed evidencebook.Record, from, to uint64) error {
	if path, _ := c.Flags().GetString("capsule-out"); path != "" {
		if err := exportRecord(c.Context(), opened.store, p, sealed.RecordID, path); err != nil {
			return err
		}
		result.Capsule = path
	}
	path, _ := c.Flags().GetString("bundle-out")
	if path == "" {
		return nil
	}
	include, err := closeBundleRecords(c.Context(), opened.book, result.Counterparty, from, to, sealed.RecordID)
	if err != nil {
		return err
	}
	// Declare the profile's key, as `bundle` and `disclose` do. Each record
	// already carries its signer's key_id beside its Producer Envelope
	// (evidencebook#84 §4), so the declaration reveals nothing more to the
	// counterparty.
	key, err := cli.PrivateKey(p.Signing)
	if err != nil {
		return err
	}
	bundle, err := opened.book.Bundle(c.Context(), evidencebook.BundleRequest{Root: sealed.RecordID, Include: include, WithholdPayloads: true, ProducerKey: key.Public().(ed25519.PublicKey)})
	if err != nil {
		return bookError(err)
	}
	if _, err = evidencebook.VerifyBundle(bundle.JSON); err != nil {
		return err
	}
	if err = cli.AtomicFile(path, bundle.JSON, false); err != nil {
		return err
	}
	result.Bundle, result.BundleDigest = path, bundle.Digest
	return nil
}

// closeBundleRecords selects what a Close's bundle discloses so that the
// counterparty can reconcile against it, and nothing more: the header of the
// record just before the Close's window (placing its start), every record in
// the window, the record just after it (placing its end), and the Close. A
// record that names a different counterparty is never disclosed; if it sits
// at either edge, the counterparty reads that edge as unproven, which is
// safe. The bundle withholds every payload; reconciliation compares the
// payload commitments the headers carry.
func closeBundleRecords(ctx context.Context, book *evidencebook.Book, counterparty string, from, to uint64, closeID string) ([]string, error) {
	records, err := book.Query(ctx, evidencebook.Filter{FromSeq: max(from, 2) - 1, ToSeq: to + 1})
	if err != nil {
		return nil, err
	}
	ids := []string{closeID}
	for _, r := range records {
		if r.RecordID != closeID && (r.Header.CounterpartyRef == "" || r.Header.CounterpartyRef == counterparty) {
			ids = append(ids, r.RecordID)
		}
	}
	return ids, nil
}

// exportRecord writes one book record as an artifact.Record: the sealed
// capsule, its producer envelope, and the canonical header it commits as
// its payload. It is verified under the profile's trusted keys before it is
// written, the same self-check `seal` applies.
func exportRecord(ctx context.Context, store *evidencebook.FileStore, p cli.Profile, recordID, path string) error {
	stored, err := store.GetRecord(ctx, recordID)
	if err != nil {
		return err
	}
	record, err := artifact.Prepare(artifact.Record{
		CapsuleID: stored.RecordID, Capsule: stored.Capsule, ProducerEnvelope: stored.Envelope,
		Artifacts: []artifact.Artifact{{Name: "payload", Binding: artifact.PayloadDigest, State: artifact.Present, Content: stored.Header}},
	})
	if err != nil {
		return err
	}
	keys, err := cli.ParseKeys(p.TrustedKeys)
	if err != nil {
		return err
	}
	if _, err = artifact.Verify(record, keys); err != nil {
		return err
	}
	encoded, err := json.Marshal(record)
	if err != nil {
		return err
	}
	return writeOutput("--capsule-out", path, encoded)
}

// -- request and respond ---------------------------------------------------

// requestCommand is `capsulectl book request` on the profile's own book: ask
// (--request), record what came back (--response), or record that nothing
// came back (--absent-until). Each commits a record before it reports. The
// flags and their checks are the core's (cli.EvidenceRequestCommand), which
// capsulectl-deal's `deal request` shares.
func requestCommand() *cobra.Command {
	cmd := cli.EvidenceRequestCommand(func(c *cobra.Command, p cli.Profile, in cli.EvidenceRequestInput) (err error) {
		// The witnesses whose receipts count on the answer's anchor
		// (ResponderKeys.Witnesses). Read before the book opens, so a bad
		// directory records nothing.
		var witnesses []evidencebook.WitnessRow
		if in.WitnessDirectory != "" {
			raw, e := os.ReadFile(in.WitnessDirectory)
			if e != nil {
				return cli.InputError("--witness-directory: " + e.Error())
			}
			if witnesses, e = evidencebook.ParseWitnessDirectory(raw); e != nil {
				return cli.InputError("--witness-directory: " + e.Error())
			}
		}
		opened, e := openBook(c.Context(), p, false)
		if e != nil {
			return e
		}
		defer func() { err = errors.Join(err, opened.release()) }()
		book := opened.book
		switch {
		case in.Request != nil:
			if in.Responder == "" || in.Output == "" {
				return cli.InputError("--request needs --responder and --output")
			}
			ask, _ := c.Flags().GetString("request")
			var sent evidencebook.SentRequest
			if asSent, _ := c.Flags().GetBool("sent"); asSent {
				octets, e := sentRequestOctets("--request "+ask, in.Request)
				if e != nil {
					return e
				}
				sent, e = book.RequestSent(c.Context(), evidencebook.Message{Octets: octets, MediaType: evidencebook.MediaTypeJSON}, in.Responder)
				if e != nil {
					return bookError(e)
				}
			} else {
				req, e := parseBookRequest("--request "+ask, in.Request)
				if e != nil {
					return e
				}
				if sent, e = book.Request(c.Context(), req, in.Responder); e != nil {
					return bookError(e)
				}
			}
			result := cli.EvidenceRequestResult{RecordID: sent.RecordID, RequestDigest: sent.Digest, Output: in.Output}
			if e = cli.AtomicFile(in.Output, sent.Bytes, false); e != nil {
				// The request is on record already: report it, and the
				// bytes to send, rather than lose them with the file.
				result.Output, result.Request = "", sent.Bytes
				return errors.Join(e, cli.Output(c, result))
			}
			return cli.Output(c, result)
		case in.Response != nil:
			// Book.Request sends JCS JSON, and a reply is in the request's
			// own encoding.
			responder := evidencebook.ResponderKeys{Signer: in.ResponderSigner, Checkpoint: in.ResponderCheckpoint, Witnesses: witnesses}
			record, e := book.RecordResponse(c.Context(), in.For, evidencebook.Message{Octets: in.Response, MediaType: evidencebook.MediaTypeJSON}, responder)
			if e != nil {
				return bookError(e)
			}
			var st evidencebook.ResponseStatement
			if e = json.Unmarshal(record.Header.Statement, &st); e != nil {
				return e
			}
			return cli.Output(c, bookResponseResult{
				EvidenceRequestResult: cli.EvidenceRequestResult{RecordID: record.RecordID, RequestDigest: st.RequestDigest, Outcome: st.Outcome, Reason: st.Reason},
				Witness:               st.Witness,
			})
		default:
			record, e := book.RecordAbsence(c.Context(), in.For, in.WindowEnd, in.Route)
			if e != nil {
				return bookError(e)
			}
			var st evidencebook.AbsenceRecord
			if e = json.Unmarshal(record.Header.Statement, &st); e != nil {
				return e
			}
			return cli.Output(c, cli.EvidenceRequestResult{RecordID: record.RecordID, RequestDigest: st.RequestDigest, Outcome: evidencebook.RecordTypeAbsence})
		}
	})
	// The core's help describes a deal copy, where a bad receipt fails the
	// answer. On a book a carried receipt is unsigned, so it can only add
	// standing (evidencebook#216).
	cmd.Flags().Bool("sent", false, "With --request: the request was already composed and sent by another tool, such as the capsules plugin. Record it as sent, member for member in JCS: every member is kept, including ones this command does not define, and no nonce or request profile is added. The answer then verifies against the request the responder answered")
	cmd.PreRunE = func(c *cobra.Command, _ []string) error {
		if asSent, _ := c.Flags().GetBool("sent"); asSent && !c.Flags().Changed("request") {
			return cli.InputError("--sent needs --request")
		}
		return nil
	}
	cmd.Flags().Lookup("witness-directory").Usage = "With --response: a witness directory (witnesses.json format). The answer reads witness \"witnessed\" only when a receipt from a witness in it covers the answer's anchor checkpoint; without it, or with no such receipt, \"unwitnessed\". A receipt that does not verify counts for nothing"
	return cmd
}

// bookResponseResult is what `book request --response` prints: the core's
// result plus the answer's witness standing, as the response record holds it
// (ResponseStatement.Witness): "witnessed" when a receipt from a witness in
// --witness-directory covers the answer's anchor, "unwitnessed" when the
// answer verifies only against its holder's own signatures, and absent when
// the answer did not verify (evidencebook#209). An unwitnessed artifact is
// never reported as witnessed.
type bookResponseResult struct {
	cli.EvidenceRequestResult
	Witness string `json:"witness,omitempty"`
}

// respondCommand is `capsulectl book respond`: answer a request with the
// book's signed artifact or refusal, under the share policy.
func respondCommand() *cobra.Command {
	return cli.EvidenceRespondCommand(func(c *cobra.Command, p cli.Profile, in cli.EvidenceRespondInput) (err error) {
		policy := evidencebook.DefaultSharePolicy()
		if in.PolicyPath != "" {
			raw, e := cli.ReadInput(in.PolicyPath)
			if e != nil {
				return e
			}
			if e = cli.DecodeJSONAs("--policy "+in.PolicyPath, raw, &policy); e != nil {
				return e
			}
		}
		opened, e := openBook(c.Context(), p, false)
		if e != nil {
			return e
		}
		defer func() { err = errors.Join(err, opened.release()) }()
		// The operator running respond is the transport: --requester is the
		// identity they authenticated the request's sender as, and the only
		// input to the book's relationship gate. Empty, only public
		// subjects are answered.
		reply, record, e := opened.book.Respond(c.Context(), evidencebook.Message{Octets: in.Request, MediaType: evidencebook.MediaTypeJSON},
			evidencebook.RespondOptions{AuthenticatedRequester: in.Requester, Policy: policy})
		if e != nil {
			return bookError(e)
		}
		encoded := reply.Octets
		result := cli.EvidenceRespondResult{RecordID: record.RecordID, Output: in.Output}
		switch {
		case record.RecordID != "":
			var st evidencebook.AnsweredStatement
			if e = json.Unmarshal(record.Header.Statement, &st); e != nil {
				return e
			}
			result.RequestDigest, result.Outcome, result.Reason, result.Relationship = st.RequestDigest, st.Outcome, st.Reason, st.Relationship
		case reply.Refusal != nil:
			// A malformed or unauthenticated request is refused without a
			// record: such traffic never grows the log.
			result.RequestDigest, result.Outcome, result.Reason = reply.Refusal.RequestDigest, evidencebook.OutcomeRefusal, reply.Refusal.Reason
		case reply.Envelope != nil:
			result.RequestDigest, result.Outcome = reply.Envelope.RequestDigest, evidencebook.OutcomeArtifact
		}
		if e = cli.AtomicFile(in.Output, encoded, false); e != nil {
			// The reply is signed (and, when record_id is set, on record)
			// already: report it rather than lose it with the file.
			result.Output, result.Response = "", encoded
			return errors.Join(e, cli.Output(c, result))
		}
		return cli.Output(c, result)
	})
}

// bookRequestInput is the request map of draft-mih-agent-evidence-request-00
// in JSON, as `request --request` reads it. evidencebook exports the request
// as a struct only, so this maps the wire members onto it; Book.Request
// checks every value and encodes the map it sends.
type bookRequestInput struct {
	Subject  map[string]json.RawMessage `json:"subject"`
	Coverage struct {
		ExpectedPin  string          `json:"expected_pin,omitempty"`
		MinFreshness json.RawMessage `json:"min_freshness,omitempty"`
	} `json:"coverage"`
	Derivation string `json:"derivation,omitempty"`
	Deadline   string `json:"deadline,omitempty"`
	Nonce      string `json:"nonce,omitempty"`
	Route      string `json:"route,omitempty"`
	Profile    string `json:"profile,omitempty"`
}

// sentRequestOctets is a request another tool sent, read from JSON as
// `request --request --sent` takes it, in the JCS it was transmitted in.
// Book.RequestSent checks it is a well-formed request.
func sentRequestOctets(label string, raw []byte) ([]byte, error) {
	value, err := evidencerequest.DecodeJSON(bytes.TrimSpace(raw))
	if err != nil {
		return nil, cli.InputError(fmt.Sprintf("%s: not one JSON value: %v", label, err))
	}
	octets, err := evidencerequest.EncodeJCS(value)
	if err != nil {
		return nil, cli.InputError(fmt.Sprintf("%s: has no JCS form: %v", label, err))
	}
	return octets, nil
}

func parseBookRequest(label string, raw []byte) (evidencebook.EvidenceRequest, error) {
	var in bookRequestInput
	if err := cli.DecodeJSONAs(label, raw, &in); err != nil {
		return evidencebook.EvidenceRequest{}, err
	}
	req := evidencebook.EvidenceRequest{
		Coverage:   evidencebook.Coverage{ExpectedPin: in.Coverage.ExpectedPin},
		Derivation: in.Derivation, Deadline: in.Deadline, Nonce: in.Nonce, Route: in.Route, Profile: in.Profile,
	}
	if len(in.Subject) != 1 {
		return req, cli.InputError(label + ": subject is a map with exactly one member, its form: full_history, checkpoints, record, exchange, range or correlation")
	}
	for form, content := range in.Subject {
		req.Subject.Kind = evidencebook.SubjectKind(form)
		var err error
		switch req.Subject.Kind {
		case evidencebook.SubjectFullHistory, evidencebook.SubjectCheckpoints:
			if string(bytes.TrimSpace(content)) != "null" {
				err = errors.New("its content is null")
			}
		case evidencebook.SubjectRecord, evidencebook.SubjectExchange:
			err = json.Unmarshal(content, &req.Subject.Digest)
		case evidencebook.SubjectRange:
			var pair []uint64
			if err = json.Unmarshal(content, &pair); err == nil && len(pair) != 2 {
				err = errors.New("its content is two log positions")
			} else if err == nil {
				req.Subject.Range = [2]uint64{pair[0], pair[1]}
			}
		case evidencebook.SubjectCorrelation:
			err = json.Unmarshal(content, &req.Subject.Correlation)
		default:
			err = errors.New("not one of the six forms")
		}
		if err != nil {
			return req, cli.InputError(fmt.Sprintf("%s: subject %q: %v", label, form, err))
		}
	}
	if floor := bytes.TrimSpace(in.Coverage.MinFreshness); len(floor) > 0 {
		req.Coverage.MinFreshness = &evidencebook.Freshness{}
		var err error
		switch {
		case string(floor) == "null":
			err = errors.New("null")
		case floor[0] == '"':
			err = json.Unmarshal(floor, &req.Coverage.MinFreshness.Time)
		default:
			var size uint64
			err = json.Unmarshal(floor, &size)
			req.Coverage.MinFreshness.Size = &size
		}
		if err != nil {
			return req, cli.InputError(label + ": min_freshness is a log size or an RFC 3339 UTC time")
		}
	}
	return req, nil
}

// clockNow is the time the book verbs decide which period has ended by: the
// reproduced book's caller-given time when SOURCE_DATE_EPOCH is set (an
// unreadable value is refused when the book opens), else the wall clock.
func clockNow() time.Time {
	if at, reproduced, _ := reproductionAt(); reproduced {
		return at
	}
	return bookNow()
}

// refuseUnderReproduction keeps a verb that stamps the wall clock into its
// records (backfill's imported_at, migrate's import time) out of a
// reproduction run, where it would break the byte-identical result.
func refuseUnderReproduction(verb string) error {
	if _, reproduced, _ := reproductionAt(); reproduced {
		return cli.InputError(fmt.Sprintf("%s stamps the wall clock into its records, so it does not run with %s set (a reproduced scratch book); unset it", verb, SourceDateEpochEnv))
	}
	return nil
}
