// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

func diffBundlePair(t *testing.T, book *Book, high, low BundleRequest) (hi, lo []byte) {
	t.Helper()
	h, err := book.Bundle(context.Background(), high)
	if err != nil {
		t.Fatal(err)
	}
	l, err := book.Bundle(context.Background(), low)
	if err != nil {
		t.Fatal(err)
	}
	return h.JSON, l.JSON
}

func TestDiffBundlesListsExactlyWhatTheLowerLevelDrops(t *testing.T) {
	book, records := linkedBook(t)
	ruling, claim, observed := records[2], records[1], records[0]

	t.Run("a narrower root drops records and their payloads", func(t *testing.T) {
		hi, lo := diffBundlePair(t, book,
			BundleRequest{Root: ruling.RecordID, Payloads: PayloadsSelected},
			BundleRequest{Root: claim.RecordID, Payloads: PayloadsSelected})
		d, err := DiffBundles(hi, lo)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(d.DroppedRecords, []string{ruling.RecordID}) {
			t.Fatalf("DroppedRecords = %v; want only the ruling", d.DroppedRecords)
		}
		if !slices.Equal(d.DroppedPayloads, sortedCopy(ruling.Header.PayloadCommitments)) || !d.Subset() {
			t.Fatalf("diff = %+v; want the ruling's payloads dropped and a subset", d)
		}
	})

	t.Run("withholding payloads drops payloads only", func(t *testing.T) {
		hi, lo := diffBundlePair(t, book,
			BundleRequest{Root: ruling.RecordID, Payloads: PayloadsSelected},
			BundleRequest{Root: ruling.RecordID, WithholdPayloads: true})
		d, err := DiffBundles(hi, lo)
		if err != nil {
			t.Fatal(err)
		}
		if len(d.DroppedRecords) != 0 || !d.Subset() {
			t.Fatalf("diff = %+v; want payloads only", d)
		}
		want := sortedCopy(slices.Concat(ruling.Header.PayloadCommitments, claim.Header.PayloadCommitments, observed.Header.PayloadCommitments))
		if !slices.Equal(d.DroppedPayloads, want) {
			t.Fatalf("DroppedPayloads = %v; want the ruling's, the claim's and the observation's payloads %v", d.DroppedPayloads, want)
		}
	})

	t.Run("suppressing the header drops the record", func(t *testing.T) {
		hi, lo := diffBundlePair(t, book,
			BundleRequest{Root: claim.RecordID, Payloads: PayloadsSelected},
			BundleRequest{Root: claim.RecordID, Payloads: PayloadsSelected, Suppress: []string{HeaderMember}})
		d, err := DiffBundles(hi, lo)
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(d.DroppedRecords, sortedCopy([]string{claim.RecordID, observed.RecordID})) || !d.Subset() {
			t.Fatalf("diff = %+v; want the claim and the observation it cites dropped (the closure)", d)
		}
	})

	t.Run("the same level has no difference", func(t *testing.T) {
		hi, lo := diffBundlePair(t, book,
			BundleRequest{Root: claim.RecordID, Payloads: PayloadsSelected},
			BundleRequest{Root: claim.RecordID, Payloads: PayloadsSelected})
		d, err := DiffBundles(hi, lo)
		if err != nil {
			t.Fatal(err)
		}
		if !d.Empty() {
			t.Fatalf("diff = %+v; want empty", d)
		}
	})

	t.Run("a level cut at a later checkpoint of the same log still compares", func(t *testing.T) {
		lo := mustBundle(t, book, BundleRequest{Root: claim.RecordID, WithholdPayloads: true})
		mustAppend(t, book, observation("order-19", `{"status":"new"}`))
		hi := mustBundle(t, book, BundleRequest{Root: claim.RecordID, Payloads: PayloadsSelected})
		d, err := DiffBundles(hi, lo)
		if err != nil {
			t.Fatal(err)
		}
		if len(d.DroppedRecords) != 0 || len(d.DroppedPayloads) == 0 || !d.Subset() {
			t.Fatalf("diff = %+v; want payloads dropped across two anchors", d)
		}
	})

	t.Run("swapped levels are not a subset", func(t *testing.T) {
		hi, lo := diffBundlePair(t, book,
			BundleRequest{Root: claim.RecordID, Payloads: PayloadsSelected},
			BundleRequest{Root: ruling.RecordID, Payloads: PayloadsSelected})
		d, err := DiffBundles(hi, lo)
		if err != nil {
			t.Fatal(err)
		}
		if d.Subset() || !slices.Contains(d.Extra, "record:"+ruling.RecordID) {
			t.Fatalf("diff = %+v; want the ruling as extra", d)
		}
	})
}

func TestDiffBundlesRefusesABundleThatDoesNotVerify(t *testing.T) {
	book, records := linkedBook(t)
	hi, lo := diffBundlePair(t, book,
		BundleRequest{Root: records[1].RecordID, Payloads: PayloadsSelected},
		BundleRequest{Root: records[1].RecordID, WithholdPayloads: true})
	if _, err := DiffBundles(hi, []byte(`{"records":[]}`)); !errors.Is(err, ErrInvalid) {
		t.Fatalf("lower: err = %v; want ErrInvalid", err)
	}
	if _, err := DiffBundles([]byte("nope"), lo); !errors.Is(err, ErrInvalid) {
		t.Fatalf("higher: err = %v; want ErrInvalid", err)
	}
}

func TestDiffBundlesRefusesBundlesOfTwoLogs(t *testing.T) {
	book, records := linkedBook(t)
	other := newFixture(t, "book-b", 1).open(t)
	otherRecord := mustAppend(t, other, observation("order-17", `{"status":"approved"}`))
	hi, err := book.Bundle(context.Background(), BundleRequest{Root: records[0].RecordID, Payloads: PayloadsSelected})
	if err != nil {
		t.Fatal(err)
	}
	lo, err := other.Bundle(context.Background(), BundleRequest{Root: otherRecord.RecordID, WithholdPayloads: true, Suppress: []string{HeaderMember}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := DiffBundles(hi.JSON, lo.JSON); !errors.Is(err, ErrInvalid) || !strings.Contains(err.Error(), "not of one log") {
		t.Fatalf("err = %v; want ErrInvalid for bundles of two logs", err)
	}
}

func mustBundle(t *testing.T, book *Book, req BundleRequest) []byte {
	t.Helper()
	b, err := book.Bundle(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	return b.JSON
}

func sortedCopy(s []string) []string {
	out := slices.Clone(s)
	slices.Sort(out)
	return out
}
