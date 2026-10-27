package parser

import (
	"fmt"
	"math"
	"math/big"
	"strconv"
	"strings"

	"github.com/pseudomuto/protokit"
	"google.golang.org/protobuf/types/descriptorpb"
)

const MaxNumericLiteralBytes = 256
const MaxNumericExponent = 1024

// ParseNumericLiteral preserves a finite annotation number without float64
// rounding. Its bounded grammar also accepts the hexadecimal and underscore
// forms already accepted by strconv.ParseFloat.
func ParseNumericLiteral(raw string) (*big.Rat, error) {
	if len(raw) == 0 || len(raw) > MaxNumericLiteralBytes {
		return nil, fmt.Errorf("numeric literal must contain 1 to %d bytes", MaxNumericLiteralBytes)
	}
	approximate, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return nil, err
	}
	if math.IsNaN(approximate) || math.IsInf(approximate, 0) {
		return nil, fmt.Errorf("expected a finite number")
	}
	if position := strings.LastIndexAny(raw, "eEpP"); position >= 0 {
		exponent, err := strconv.ParseInt(strings.ReplaceAll(raw[position+1:], "_", ""), 10, 32)
		if err != nil || exponent < -MaxNumericExponent || exponent > MaxNumericExponent {
			return nil, fmt.Errorf("numeric exponent must be between -%d and %d", MaxNumericExponent, MaxNumericExponent)
		}
	}
	value, ok := new(big.Rat).SetString(raw)
	if !ok {
		return nil, fmt.Errorf("invalid numeric literal %q", raw)
	}
	return value, nil
}

// ResolveIntegerRange turns inclusive rational constraints into an inclusive
// integer interval. Nil endpoints retain the example generator's small default
// range, adjusted when an explicit constraint lies beyond that default.
func ResolveIntegerRange(minimum, maximum *big.Rat, bits int, unsigned bool) (*big.Int, *big.Int, error) {
	if bits != 32 && bits != 64 {
		return nil, nil, fmt.Errorf("unsupported integer width %d", bits)
	}
	domainMinimum := new(big.Int)
	domainMaximum := new(big.Int).Lsh(big.NewInt(1), uint(bits))
	label := "unsigned"
	if !unsigned {
		label = "signed"
		domainMaximum.Rsh(domainMaximum, 1)
		domainMinimum.Neg(domainMaximum)
	}
	domainMaximum.Sub(domainMaximum, big.NewInt(1))
	lower, upper := big.NewInt(0), big.NewInt(1000000)
	if minimum != nil {
		lower = roundInteger(minimum, true)
	}
	if maximum != nil {
		upper = roundInteger(maximum, false)
	}
	if minimum != nil && (lower.Cmp(domainMinimum) < 0 || lower.Cmp(domainMaximum) > 0) || maximum != nil && (upper.Cmp(domainMinimum) < 0 || upper.Cmp(domainMaximum) > 0) {
		return nil, nil, fmt.Errorf("integer bounds are outside the %s %d-bit range", label, bits)
	}
	if minimum == nil && upper.Cmp(lower) < 0 {
		lower = new(big.Int).Set(upper)
	}
	if maximum == nil && lower.Cmp(upper) > 0 {
		upper = new(big.Int).Set(lower)
	}
	if lower.Cmp(upper) > 0 {
		return nil, nil, fmt.Errorf("integer bounds contain no representable %s %d-bit value", label, bits)
	}
	return lower, upper, nil
}

func validateIntegerFieldBounds(descriptor *protokit.FieldDescriptor, flags *FieldFlags) error {
	if !flags.minLiteral.Present() && !flags.maxLiteral.Present() {
		return nil
	}
	valueType := protoToFieldValueType(descriptor)
	if flags.customType.Present() {
		valueType = *flags.customType.Get()
	}
	if valueType != ValueTypeInt && valueType != ValueTypeUInt {
		return nil
	}
	bits := 64
	switch descriptor.GetType() {
	case descriptorpb.FieldDescriptorProto_TYPE_INT32, descriptorpb.FieldDescriptorProto_TYPE_SINT32,
		descriptorpb.FieldDescriptorProto_TYPE_SFIXED32, descriptorpb.FieldDescriptorProto_TYPE_UINT32,
		descriptorpb.FieldDescriptorProto_TYPE_FIXED32:
		bits = 32
	}
	var minimum, maximum *big.Rat
	if flags.minLiteral.Present() {
		minimum, _ = ParseNumericLiteral(*flags.minLiteral.Get())
	}
	if flags.maxLiteral.Present() {
		maximum, _ = ParseNumericLiteral(*flags.maxLiteral.Get())
	}
	_, _, err := ResolveIntegerRange(minimum, maximum, bits, valueType == ValueTypeUInt)
	return err
}

func roundInteger(value *big.Rat, ceil bool) *big.Int {
	quotient, remainder := new(big.Int), new(big.Int)
	quotient.QuoRem(value.Num(), value.Denom(), remainder)
	if remainder.Sign() != 0 {
		if ceil && value.Sign() > 0 {
			quotient.Add(quotient, big.NewInt(1))
		} else if !ceil && value.Sign() < 0 {
			quotient.Sub(quotient, big.NewInt(1))
		}
	}
	return quotient
}
