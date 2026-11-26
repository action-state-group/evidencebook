// SPDX-License-Identifier: Apache-2.0

package xdealv1

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNormalOrigin(t *testing.T) {
	for raw, want := range map[string]string{
		"https://Shop.Example":     "https://shop.example",
		"https://shop.example/":    "https://shop.example",
		"https://shop.example:443": "https://shop.example",
		"http://shop.example:80":   "http://shop.example",
		" https://bücher.example ": "https://xn--bcher-kva.example",
	} {
		got, err := NormalOrigin(raw)
		require.NoError(t, err, raw)
		assert.Equal(t, want, got, raw)
	}
	for raw, why := range map[string]string{
		"ftp://shop.example":        "its scheme is not https or http",
		"https://u@shop.example":    "it carries user info",
		"https://shop.example/?a=1": "it carries a query",
		"https://shop.example/#a":   "it carries a fragment",
		"https://shop.example/cart": "it carries a path",
		"https://shop.example:8443": "it names a port other than the scheme's own",
		"https://":                  "it names no host",
	} {
		_, err := NormalOrigin(raw)
		assert.EqualError(t, err, raw+" is not a checkout origin: "+why)
	}
}

func TestCardLike(t *testing.T) {
	assert.True(t, CardLike("4111 1111 1111 1111"))
	assert.True(t, CardLike("5500-0000-0000-0004"))
	assert.False(t, CardLike("4111 1111 1111 1112"), "fails the checksum")
	assert.False(t, CardLike("1002003004005005"), "UATP's 1 is no card prefix here")
	assert.False(t, CardLike("A-1002"), "not digits")
	assert.False(t, CardLike("411111111111"), "too short")
	assert.False(t, CardPrefix(""))
}
