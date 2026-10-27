package engine

import (
	cryptoRand "crypto/rand"
	"fmt"
	"math/big"
	"strconv"

	"github.com/kordax/pb-md5-generator/internal/parser"
	"google.golang.org/protobuf/types/descriptorpb"
)

func exampleIntegerBits(field MessageField) int {
	switch field.d.GetType() {
	case descriptorpb.FieldDescriptorProto_TYPE_INT32,
		descriptorpb.FieldDescriptorProto_TYPE_SINT32,
		descriptorpb.FieldDescriptorProto_TYPE_SFIXED32,
		descriptorpb.FieldDescriptorProto_TYPE_UINT32,
		descriptorpb.FieldDescriptorProto_TYPE_FIXED32:
		return 32
	default:
		return 64
	}
}

func (g *Codegenerator) signedExample(field MessageField, flags FieldFlags) (int64, error) {
	bits := exampleIntegerBits(field)
	if flags.value.Present() {
		return strconv.ParseInt(*flags.value.Get(), 10, bits)
	}
	minimum, maximum, err := integerExampleRange(flags, bits, false)
	if err != nil {
		return 0, err
	}
	value, err := g.sampleInteger(minimum, maximum)
	if err != nil {
		return 0, err
	}
	return value.Int64(), nil
}

func (g *Codegenerator) unsignedExample(field MessageField, flags FieldFlags) (uint64, error) {
	bits := exampleIntegerBits(field)
	if flags.value.Present() {
		return strconv.ParseUint(*flags.value.Get(), 10, bits)
	}
	minimum, maximum, err := integerExampleRange(flags, bits, true)
	if err != nil {
		return 0, err
	}
	value, err := g.sampleInteger(minimum, maximum)
	if err != nil {
		return 0, err
	}
	return value.Uint64(), nil
}

func integerExampleRange(flags FieldFlags, bits int, unsigned bool) (*big.Int, *big.Int, error) {
	minimum, err := exampleBound(flags.minLiteral, flags.min)
	if err != nil {
		return nil, nil, err
	}
	maximum, err := exampleBound(flags.maxLiteral, flags.max)
	if err != nil {
		return nil, nil, err
	}
	return parser.ResolveIntegerRange(minimum, maximum, bits, unsigned)
}

func exampleBound(literal Option[string], approximate Option[float64]) (*big.Rat, error) {
	if literal.Present() {
		return parser.ParseNumericLiteral(*literal.Get())
	}
	if approximate.Present() {
		value := new(big.Rat).SetFloat64(*approximate.Get())
		if value == nil {
			return nil, fmt.Errorf("numeric bound must be finite")
		}
		return value, nil
	}
	return nil, nil
}

// Annotation bounds are inclusive. A big.Int span also represents 2^64, so the
// full uint64 or int64 domain can be sampled without overflowing an endpoint.
func (g *Codegenerator) sampleInteger(minimum, maximum *big.Int) (*big.Int, error) {
	if minimum.Cmp(maximum) == 0 {
		return new(big.Int).Set(minimum), nil
	}
	span := new(big.Int).Sub(maximum, minimum)
	span.Add(span, big.NewInt(1))
	value, err := cryptoRand.Int(g.random, span)
	if err != nil {
		return nil, err
	}
	return value.Add(value, minimum), nil
}
