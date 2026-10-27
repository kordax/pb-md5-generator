package importer

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/pb33f/jsonschema/v6"
	"github.com/pb33f/libopenapi/datamodel"
)

type contractValidator struct {
	contract *jsonschema.Schema
	schema   *jsonschema.Schema
	legacy   bool
}

func newContractValidator(version string) (*contractValidator, error) {
	var encoded string
	switch {
	case strings.HasPrefix(version, "3.0."):
		encoded = datamodel.OpenAPI3SchemaData
	case strings.HasPrefix(version, "3.1."):
		encoded = datamodel.OpenAPI31SchemaData
	case strings.HasPrefix(version, "3.2."):
		encoded = datamodel.OpenAPI32SchemaData
	default:
		return nil, fmt.Errorf("unsupported OpenAPI version")
	}
	decoder := json.NewDecoder(strings.NewReader(encoded))
	decoder.UseNumber()
	var definition any
	if err := decoder.Decode(&definition); err != nil {
		return nil, err
	}
	compiler := jsonschema.NewCompiler()
	compiler.UseLoader(deniedLoader{})
	if err := compiler.AddResource("urn:pb-docs:openapi", definition); err != nil {
		return nil, err
	}
	contract, err := compiler.Compile("urn:pb-docs:openapi")
	if err != nil {
		return nil, err
	}
	schema, err := compiler.Compile("https://json-schema.org/draft/2020-12/schema")
	if err != nil {
		return nil, err
	}
	return &contractValidator{contract: contract, schema: schema, legacy: strings.HasPrefix(version, "3.0.")}, nil
}

func (v *contractValidator) validateDocument(value any) (string, error) {
	err := v.contract.Validate(value)
	if err == nil {
		return "", nil
	}
	var failure *jsonschema.ValidationError
	if errors.As(err, &failure) {
		for len(failure.Causes) == 1 {
			failure = failure.Causes[0]
		}
		var pointer strings.Builder
		for _, part := range failure.InstanceLocation {
			pointer.WriteByte('/')
			pointer.WriteString(escapePointer(part))
		}
		return pointer.String(), err
	}
	return "", err
}

func (v *contractValidator) validateSchema(value any, version string) error {
	if v.legacy {
		wrapper := map[string]any{"openapi": version, "info": map[string]any{"title": "Schema", "version": "1"}, "paths": map[string]any{}, "components": map[string]any{"schemas": map[string]any{"Schema": value}}}
		return v.contract.Validate(wrapper)
	}
	mapping := object(value)
	if dialect := text(mapping["$schema"]); dialect != "" && !supportedDialect(dialect) {
		return fmt.Errorf("unsupported JSON Schema dialect")
	}
	// OAS 3.1/3.2 use JSON Schema 2020-12 plus annotation keywords. Meta-
	// validation checks keyword shapes without resolving or executing source refs.
	return v.schema.Validate(value)
}

func (v *contractValidator) validateObject(kind string, value any, version string) error {
	wrapper := map[string]any{
		"openapi":    version,
		"info":       map[string]any{"title": "Object", "version": "1"},
		"paths":      map[string]any{},
		"components": map[string]any{kind: map[string]any{"Object": value}},
	}
	return v.contract.Validate(wrapper)
}

func (v *contractValidator) validatePathItem(value any, version string) error {
	wrapper := map[string]any{
		"openapi": version,
		"info":    map[string]any{"title": "Path", "version": "1"},
		"paths":   map[string]any{"/_docs": value},
	}
	return v.contract.Validate(wrapper)
}
