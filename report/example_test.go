// SPDX-License-Identifier: Apache-2.0

package report_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/action-state-group/evidencebook"
	"github.com/action-state-group/evidencebook/report"
)

// Build renders a verified Result-rooted bundle under a view. This bundle is
// the monthly-outcome presentation fixture: Build writes exactly the page
// committed beside it.
func ExampleBuild() {
	bundle, err := os.ReadFile("testdata/presentation/4-monthly-outcome/bundle.json")
	if err != nil {
		log.Fatal(err)
	}
	page, err := report.Build(bundle, report.View{Card: "outcome"})
	if err != nil {
		log.Fatal(err)
	}
	committed, err := os.ReadFile("testdata/presentation/4-monthly-outcome/page.html")
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("verdict:", page.Verdict)
	fmt.Println("form:", page.Form, page.Member)
	fmt.Println("claims:", page.Claims, "unsupported:", page.UnsupportedClaims)
	fmt.Println("same bytes as the committed page:", bytes.Equal(page.HTML, committed))
	// Output:
	// verdict: VALID
	// form: payload agent_input
	// claims: 32 unsupported: 0
	// same bytes as the committed page: true
}

// A host's wording pack is laid over the viewer's own words, and the
// contract report is selected for the Result. The pack is bound to its
// bytes by its SHA-256.
func ExampleBuild_wording() {
	bundle, err := os.ReadFile("testdata/presentation/4-monthly-outcome/bundle.json")
	if err != nil {
		log.Fatal(err)
	}
	pack := `{"wording_pack_version":"aac.wording-pack/v0","id":"example.host/v0","locale":"en","entries":{"page.title":"Monthly outcomes"}}`
	sum := sha256.Sum256([]byte(pack))
	page, err := report.Build(bundle, report.View{
		Card:           "outcome",
		Wording:        &report.Wording{Pack: pack, SHA256: hex.EncodeToString(sum[:])},
		ContractReport: true,
	})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("verdict:", page.Verdict)
	fmt.Println("titled by the pack:", bytes.Contains(page.HTML, []byte("<title>Monthly outcomes</title>")))
	// Output:
	// verdict: VALID
	// titled by the pack: true
}

// A bundle that verifies but is not VALID by `verify --bundle` gets no page:
// Build still reports what it found, with the page gate's refusal.
func ExampleBuild_incomplete() {
	bundle, err := os.ReadFile("testdata/incomplete-payload-bundle.json")
	if err != nil {
		log.Fatal(err)
	}
	page, err := report.Build(bundle, report.View{Card: "outcome"})
	fmt.Println("rendered:", err == nil && page.HTML != nil)
	fmt.Println("verdict:", page.Verdict)
	fmt.Println("unmet:", page.UnmetClaims)
	// Output:
	// rendered: false
	// verdict: INCOMPLETE
	// unmet: [checkpoint withheld producer_signatures withheld]
}

// SealPage records a page's digest in the holder's book; CheckPage later
// tells whether a page file is one the book sealed. This bundle is a
// book-form Result root, disclosed with its own capsules.
func ExampleSealPage() {
	ctx := context.Background()
	dir, err := os.MkdirTemp("", "report-example")
	if err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(dir)
	book, err := openBook(ctx, dir)
	if err != nil {
		log.Fatal(err)
	}
	defer book.Release()

	bundle, err := os.ReadFile("testdata/book-form-bundle.json")
	if err != nil {
		log.Fatal(err)
	}
	page, err := report.Build(bundle, report.View{Card: "obligation", Presentation: &report.Presentation{Title: "Week 38"}})
	if err != nil {
		log.Fatal(err)
	}
	sealed, err := report.SealPage(ctx, book, page.HTML)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("checkpoint cut:", sealed.Checkpoint > 0, "already sealed:", sealed.AlreadySealed)

	check, err := report.CheckPage(ctx, book, page.HTML)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("untouched page:", check.Outcome, check.Verdict)

	changed := bytes.Replace(page.HTML, []byte("<!doctype html>"), []byte("<!DOCTYPE html>"), 1)
	check, err = report.CheckPage(ctx, book, changed)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Println("changed page:", check.Outcome, check.Verdict)
	// Output:
	// checkpoint cut: true already sealed: false
	// untouched page: match VALID
	// changed page: mismatch VALID
}

// openBook is a book on disk under dir, as any holder opens one.
func openBook(ctx context.Context, dir string) (*evidencebook.Book, error) {
	key := func(seed byte) ed25519.PrivateKey {
		return ed25519.NewKeyFromSeed(bytes.Repeat([]byte{seed}, ed25519.SeedSize))
	}
	store, err := evidencebook.OpenFileStore(filepath.Join(dir, "records"))
	if err != nil {
		return nil, err
	}
	substrate, err := evidencebook.OpenCLL(filepath.Join(dir, "cll.jsonl"), "example", key(2))
	if err != nil {
		return nil, err
	}
	signer, err := evidencebook.NewEd25519Signer(key(1))
	if err != nil {
		return nil, err
	}
	originals, err := evidencebook.OpenOriginalsFile(ctx, filepath.Join(dir, "originals.jsonl"), "example", signer.PublicKey())
	if err != nil {
		return nil, err
	}
	return evidencebook.Open(ctx, evidencebook.Config{
		BookID: "example", Operator: "example-operator", Store: store, Substrate: substrate, Originals: originals, Signer: signer,
		Now: func() time.Time { return time.Date(2026, 10, 27, 12, 0, 0, 0, time.UTC) },
	})
}
