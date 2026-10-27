package importer

import (
	"encoding/json"

	"github.com/kordax/pb-md5-generator/document"
)

// JSON string companions survive browser JSON.parse without rounding numbers.
// RawMessage remains the authoritative machine-readable value.
func preserveExampleJSON(api *document.API) {
	for schemaIndex := range api.Schemas {
		schema := &api.Schemas[schemaIndex]
		var fields map[string]json.RawMessage
		if json.Unmarshal(schema.Definition, &fields) != nil {
			continue
		}
		schema.ExampleJSON = string(fields["example"])
		if schema.ExampleJSON == "" {
			var examples []json.RawMessage
			if json.Unmarshal(fields["examples"], &examples) == nil && len(examples) > 0 {
				schema.ExampleJSON = string(examples[0])
			}
		}
		schema.DefaultJSON = string(fields["default"])
	}
	for index := range api.Operations {
		operation := &api.Operations[index]
		preserveParameterExamples(operation.Parameters)
		if operation.RequestBody != nil {
			preserveMediaExamples(operation.RequestBody.Content)
		}
		for responseIndex := range operation.Responses {
			response := &operation.Responses[responseIndex]
			preserveParameterExamples(response.Headers)
			preserveMediaExamples(response.Content)
		}
	}
}

func preserveParameterExamples(parameters []document.Parameter) {
	for index := range parameters {
		parameter := &parameters[index]
		parameter.ExampleJSON = string(parameter.Example)
		preserveNamedExamples(parameter.Examples)
		preserveMediaExamples(parameter.Content)
	}
}

func preserveMediaExamples(mediaTypes []document.MediaType) {
	for index := range mediaTypes {
		media := &mediaTypes[index]
		media.ExampleJSON = string(media.Example)
		preserveNamedExamples(media.Examples)
	}
}

func preserveNamedExamples(examples []document.Example) {
	for index := range examples {
		example := &examples[index]
		if len(example.DataValue) > 0 {
			example.ValueJSON = string(example.DataValue)
		} else {
			example.ValueJSON = string(example.Value)
		}
	}
}
