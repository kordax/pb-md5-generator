package engine

import (
	"encoding/json"
	"math"
	"math/big"
	"strings"
	"testing"

	"github.com/kordax/pb-md5-generator/engine/render"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/types/dynamicpb"
)

func TestExactNumericAnnotationsReachExamplesAndMarkdown(t *testing.T) {
	files, request := parseExampleSource(t, `syntax = "proto3";
// @autocode[json]
message Exact {
  // @doc min=9007199254740993 max=9007199254740993
  int64 precise = 1;
  // @doc min=-9223372036854775808 max=-9223372036854775808
  int64 lowest = 2;
  // @doc min=9223372036854775807 max=9223372036854775807
  int64 highest = 3;
  // @doc min=18446744073709551615 max=18446744073709551615
  uint64 unsigned_highest = 4;
  // @doc min=1e1 max=1e1
  int32 exponent = 5;
  // @doc min=1.2 max=2.1
  int32 fractional = 6;
  // @doc min=-2.1 max=-1.2
  int32 negative_fractional = 7;
  // @doc min=1e-400 max=1.1
  uint32 tiny_minimum = 8;
  // @doc min=0x20000000000001p0 max=0x20000000000001p0
  int64 hexadecimal = 9;
  // @doc min=0.5 max=0.5
  double fixed_float = 10;
}`)
	message := exampleMessage(t, files, "Exact")
	flags := message.fields[0].flags.Get()
	assert.Equal(t, "9007199254740993", *flags.GetMinLiteral().Get())
	assert.Equal(t, "9007199254740993", *flags.GetMaxLiteral().Get())
	block, err := NewCodegenerator().Generate(files, message)
	require.NoError(t, err)
	var envelope map[string]json.RawMessage
	require.NoError(t, json.Unmarshal([]byte(block.GetText()), &envelope))
	var values map[string]json.Number
	decoder := json.NewDecoder(strings.NewReader(string(envelope["Exact"])))
	decoder.UseNumber()
	require.NoError(t, decoder.Decode(&values))
	for field, expected := range map[string]string{
		"precise": "9007199254740993", "lowest": "-9223372036854775808", "highest": "9223372036854775807",
		"unsigned_highest": "18446744073709551615", "exponent": "10", "fractional": "2",
		"negative_fractional": "-2", "tiny_minimum": "1", "hexadecimal": "9007199254740993", "fixed_float": "0.5",
	} {
		assert.Equal(t, expected, values[field].String(), field)
	}
	file, err := protodesc.NewFile(request.ProtoFile[0], nil)
	require.NoError(t, err)
	require.NoError(t, protojson.Unmarshal(envelope["Exact"], dynamicpb.NewMessage(file.Messages().ByName("Exact"))))
	document, err := NewMDGenerator(NewCodegenerator()).Generate(files)
	require.NoError(t, err)
	markdown, err := render.NewMarkdownRenderer(render.DefaultConfig()).Render(document)
	require.NoError(t, err)
	for _, literal := range []string{"9007199254740993", "18446744073709551615", "9223372036854775807", "1e-400", "0x20000000000001p0"} {
		assert.Contains(t, markdown, literal)
	}
}

func TestInclusiveIntegerSamplingCanReachEachEndpoint(t *testing.T) {
	files, _ := parseExampleSource(t, `syntax = "proto3";
// @autocode[json]
message Exact {
  // @doc min=9007199254740993 max=9007199254740994
  int64 signed = 1;
  // @doc min=18446744073709551614 max=18446744073709551615
  uint64 unsigned = 2;
  // @doc min=-9223372036854775808 max=9223372036854775807
  int64 full_signed = 3;
  // @doc min=0 max=18446744073709551615
  uint64 full_unsigned = 4;
}`)
	message := exampleMessage(t, files, "Exact")
	generator := NewCodegenerator()
	seen := map[string]bool{}
	for range 100 {
		block, err := generator.Generate(files, message)
		require.NoError(t, err)
		var envelope map[string]json.RawMessage
		require.NoError(t, json.Unmarshal([]byte(block.GetText()), &envelope))
		var values map[string]json.Number
		require.NoError(t, json.Unmarshal(envelope["Exact"], &values))
		seen[values["signed"].String()], seen[values["unsigned"].String()] = true, true
		_, ok := new(big.Int).SetString(values["full_signed"].String(), 10)
		assert.True(t, ok)
		_, err = values["full_signed"].Int64()
		require.NoError(t, err)
	}
	for _, endpoint := range []string{"9007199254740993", "9007199254740994", "18446744073709551614", "18446744073709551615"} {
		assert.True(t, seen[endpoint], endpoint)
	}
}

func TestLegacyFloatBoundsRemainSupported(t *testing.T) {
	minimum, maximum, err := integerExampleRange(FieldFlags{min: Some(float64(math.MinInt64))}, 64, false)
	require.NoError(t, err)
	assert.Equal(t, "-9223372036854775808", minimum.String())
	assert.Equal(t, "1000000", maximum.String())
	_, _, err = integerExampleRange(FieldFlags{min: Some(math.NaN())}, 64, false)
	require.ErrorContains(t, err, "finite")
}
