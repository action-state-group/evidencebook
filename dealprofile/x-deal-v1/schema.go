// SPDX-License-Identifier: Apache-2.0

// Package xdealv1files ships the x-deal-v1 profile's files (its record
// schema) to Go code. Package xdealv1 reads them; a reader in another
// language reads the files themselves.
package xdealv1files

import _ "embed"

// Schema is x-deal-v1.schema.json.
//
//go:embed x-deal-v1.schema.json
var Schema []byte
