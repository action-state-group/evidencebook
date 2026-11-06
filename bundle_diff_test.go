// SPDX-License-Identifier: Apache-2.0

package evidencebook

import (
	"context"
	"errors"
	"slices"
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
		if !slices.Contains(d.DroppedPayloads, observed.Header.PayloadCommitments[0]) {
			t.Fatalf("DroppedPayloads = %v; want the observation's payload", d.DroppedPayloads)
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
		if !!slices.Equal(d.DroppedRecords, []string{claim.RecordID}) || !d.Subset() {
			t.Fatalf("diff = %+v; want only the claim dropped", d)
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

func sortedCopy(s []string) []string {
	out := slices.Clone(s)
	slices.Sort(out)
	return out
}
