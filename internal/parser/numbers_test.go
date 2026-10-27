package parser

import (
	"math/big"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNumericLiteralsPreserveExactValues(t *testing.T) {
	for _, input := range []string{"9007199254740993", "9_007_199_254_740_993", "0x20000000000001p0", "900719925474099.3e1"} {
		value, err := ParseNumericLiteral(input)
		require.NoError(t, err)
		assert.Equal(t, "9007199254740993", value.RatString())
	}
	value, err := ParseNumericLiteral("1e-1024")
	require.NoError(t, err)
	assert.Positive(t, value.Sign())
	assert.Less(t, value.Num().BitLen()+value.Denom().BitLen(), 4096)
	for _, input := range []string{"", "NaN", "+Inf", "-Infinity", "1/2", "not-a-number", "1e-1025", "0x1p-1025", "1e999999999999999999999", strings.Repeat("0", MaxNumericLiteralBytes+1)} {
		_, err := ParseNumericLiteral(input)
		require.Error(t, err, input)
	}
}

func TestIntegerRangesAreInclusiveAndExact(t *testing.T) {
	for _, test := range []struct {
		minimum, maximum string
		bits             int
		unsigned         bool
		lower, upper     string
	}{
		{"-9223372036854775808", "9223372036854775807", 64, false, "-9223372036854775808", "9223372036854775807"},
		{"0", "18446744073709551615", 64, true, "0", "18446744073709551615"},
		{"9007199254740993", "9007199254740993", 64, false, "9007199254740993", "9007199254740993"},
		{"1.2", "2.1", 32, false, "2", "2"},
		{"-2.1", "-1.2", 32, false, "-2", "-2"},
		{"1e-400", "1.1", 32, true, "1", "1"},
		{"", "-9223372036854775808", 64, false, "-9223372036854775808", "-9223372036854775808"},
		{"18446744073709551615", "", 64, true, "18446744073709551615", "18446744073709551615"},
		{"", "", 32, false, "0", "1000000"},
	} {
		t.Run(test.minimum+".."+test.maximum, func(t *testing.T) {
			var minimum, maximum *big.Rat
			var err error
			if test.minimum != "" {
				minimum, err = ParseNumericLiteral(test.minimum)
				require.NoError(t, err)
			}
			if test.maximum != "" {
				maximum, err = ParseNumericLiteral(test.maximum)
				require.NoError(t, err)
			}
			lower, upper, err := ResolveIntegerRange(minimum, maximum, test.bits, test.unsigned)
			require.NoError(t, err)
			assert.Equal(t, test.lower, lower.String())
			assert.Equal(t, test.upper, upper.String())
		})
	}
	for _, test := range []struct {
		minimum, maximum string
		bits             int
		unsigned         bool
	}{
		{"1.2", "1.8", 32, false}, {"-1", "2", 64, true},
		{"9223372036854775808", "", 64, false}, {"", "18446744073709551616", 64, true},
		{"0", "1", 16, true},
	} {
		var minimum, maximum *big.Rat
		if test.minimum != "" {
			minimum, _ = ParseNumericLiteral(test.minimum)
		}
		if test.maximum != "" {
			maximum, _ = ParseNumericLiteral(test.maximum)
		}
		_, _, err := ResolveIntegerRange(minimum, maximum, test.bits, test.unsigned)
		require.Error(t, err)
	}
}

func FuzzNumericLiteralBounds(f *testing.F) {
	for _, input := range []string{"9007199254740993", "1e-1024", "1e-1000000000", "0x1p-5", "NaN", "1_000"} {
		f.Add(input)
	}
	f.Fuzz(func(t *testing.T, input string) {
		value, err := ParseNumericLiteral(input)
		if err != nil {
			return
		}
		require.NotNil(t, value)
		assert.Less(t, value.Num().BitLen()+value.Denom().BitLen(), 8192)
	})
}
