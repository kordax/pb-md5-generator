package password

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPasswordGenerator_InstancePassword(t *testing.T) {
	minDec := 4
	minChar := 3
	minSpec := 2
	gen := NewGenerator(1000, minDec, minChar, minSpec)
	uniq := make([]string, 0)

	for i := 0; i < 1000; i++ {
		password := gen.GetPassword()
		assert.NotEmpty(t, password)
		assert.Len(t, password, minDec+minChar+minSpec)
		uniq = append(uniq, password)
	}

	for i, u := range uniq {
		uCnt := 1
		for i := i + 1; i < len(uniq); i++ {
			if uniq[i] == u {
				uCnt++
			}
		}
		assert.Equal(t, 1, uCnt, "password expected to be unique: "+u)
	}
}
