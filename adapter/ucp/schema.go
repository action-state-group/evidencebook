// SPDX-License-Identifier: Apache-2.0

package ucp

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"golang.org/x/text/language"
	"golang.org/x/text/message"
)

// The UCP JSON schemas, vendored unchanged from
// github.com/Universal-Commerce-Protocol/ucp at tag v2026-08-25, commit
// cd78fb38e819de77d9b527d110476eccb876f1bd, path source/schemas/: the
// transitive $ref closure of common/payment_ap2_mandate.json and
// shopping/order.json (47 files, Apache-2.0, LICENSE alongside). SHA256SUMS
// lists every file, and its own SHA-256 is pinned here; the adapter refuses
// to validate against schemas that do not match it.
const (
	UCPVersion        = "v2026-08-25"
	UCPCommit         = "cd78fb38e819de77d9b527d110476eccb876f1bd"
	schemaDir         = "schema/v2026-08-25"
	schemaSumsSHA256  = "b794646cfd559aad036a9856ccaefd8e93eb291c565f18024de8309b2969a640"
	schemaBase        = "https://ucp.dev/schemas/"
	checkoutSchemaURL = schemaBase + "common/payment_ap2_mandate.json#/$defs/dev.ucp.shopping.checkout"
	orderSchemaURL    = schemaBase + "shopping/order.json"

	// Validator names the JSON Schema implementation and its settings.
	Validator = "github.com/santhosh-tekuri/jsonschema/v6 v6.0.3, draft 2020-12, format as annotation (the 2020-12 default), Go regexp, no network loader"
)

//go:embed schema/v2026-08-25
var schemaFS embed.FS

var (
	schemaOnce     sync.Once
	checkoutSchema *jsonschema.Schema
	orderSchema    *jsonschema.Schema
	schemaErr      error
)

type noLoader struct{}

func (noLoader) Load(url string) (any, error) {
	return nil, fmt.Errorf("schema %s is not vendored; the adapter loads nothing from the network", url)
}

// verifySchemaSums checks every vendored file against SHA256SUMS, and
// SHA256SUMS against the pinned digest. It returns the listed files.
func verifySchemaSums() ([]string, error) {
	sums, err := schemaFS.ReadFile(schemaDir + "/SHA256SUMS")
	if err != nil {
		return nil, err
	}
	if s := sha256.Sum256(sums); hex.EncodeToString(s[:]) != schemaSumsSHA256 {
		return nil, fmt.Errorf("%s/SHA256SUMS does not match the pinned digest", schemaDir)
	}
	listed := map[string]bool{}
	var files []string
	for _, line := range strings.Split(strings.TrimSpace(string(sums)), "\n") {
		want, name, ok := strings.Cut(line, "  ")
		if !ok {
			return nil, fmt.Errorf("SHA256SUMS: malformed line %q", line)
		}
		data, err := schemaFS.ReadFile(schemaDir + "/" + name)
		if err != nil {
			return nil, err
		}
		if s := sha256.Sum256(data); hex.EncodeToString(s[:]) != want {
			return nil, fmt.Errorf("vendored schema %s does not match SHA256SUMS", name)
		}
		listed[name] = true
		files = append(files, name)
	}
	err = fs.WalkDir(schemaFS, schemaDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".json") {
			return err
		}
		if name := strings.TrimPrefix(p, schemaDir+"/"); !listed[name] {
			return fmt.Errorf("vendored schema %s is not in SHA256SUMS", name)
		}
		return nil
	})
	return files, err
}

func loadSchemas() {
	files, err := verifySchemaSums()
	if err != nil {
		schemaErr = err
		return
	}
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	c.UseLoader(noLoader{})
	for _, name := range files {
		data, _ := schemaFS.ReadFile(schemaDir + "/" + name)
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(data))
		if err != nil {
			schemaErr = fmt.Errorf("%s: %w", name, err)
			return
		}
		if err := c.AddResource(schemaBase+name, doc); err != nil {
			schemaErr = err
			return
		}
	}
	if checkoutSchema, err = c.Compile(checkoutSchemaURL); err != nil {
		schemaErr = err
		return
	}
	orderSchema, schemaErr = c.Compile(orderSchemaURL)
}

// validate validates a value returned by ijson.Parse against the checkout
// (AP2-extended) or order schema. It returns a one-line reason on a
// violation.
func validate(order bool, v any) (ok bool, reason string, err error) {
	schemaOnce.Do(loadSchemas)
	if schemaErr != nil {
		return false, "", schemaErr
	}
	sch := checkoutSchema
	if order {
		sch = orderSchema
	}
	if verr := sch.Validate(v); verr != nil {
		if ve, isVE := verr.(*jsonschema.ValidationError); isVE {
			return false, firstLeaf(ve), nil
		}
		return false, verr.Error(), nil
	}
	return true, "", nil
}

// firstLeaf returns the deepest first cause, which names the member at fault.
func firstLeaf(ve *jsonschema.ValidationError) string {
	for len(ve.Causes) > 0 {
		ve = ve.Causes[0]
	}
	return "/" + strings.Join(ve.InstanceLocation, "/") + ": " + ve.ErrorKind.LocalizedString(message.NewPrinter(language.English))
}
