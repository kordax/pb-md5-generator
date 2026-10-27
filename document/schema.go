package document

import (
	"embed"
	"fmt"
)

//go:embed schema/*-v1.json
var schemaFiles embed.FS

// JSONSchema returns a standalone JSON Schema Draft 2020-12 contract for the
// version 1 draft manifest, project or page format. Each call owns its bytes.
func JSONSchema(kind string) ([]byte, error) {
	switch kind {
	case "manifest", "page", "project":
		return schemaFiles.ReadFile("schema/" + kind + "-v1.json")
	default:
		return nil, fmt.Errorf("unknown documentation schema %q", kind)
	}
}

// SchemaNames returns the supported schema kinds in stable sorted order.
func SchemaNames() []string {
	return []string{"manifest", "page", "project"}
}
