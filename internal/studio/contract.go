package studio

import (
	"bytes"
	_ "embed"
)

//go:embed api/openapi.yaml
var apiContract []byte

// APIContract returns the draft CMS HTTP contract as an independent byte slice.
func APIContract() []byte {
	return bytes.Clone(apiContract)
}
