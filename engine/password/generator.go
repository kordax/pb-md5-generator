package password

import (
	"crypto/rand"
	"fmt"
	"io"
	"math"
	"math/big"
	"strings"
	"sync"
)

const SpecialChars = "!@#$%^&*()[]"

type Generator struct {
	reader       io.Reader
	passwords    []string
	queries      int
	maxCacheSize int

	minDec  int
	minChar int
	minSpec int

	mtx sync.Mutex
}

func NewGenerator(cacheSize, minDec, minChar, minSpec int) *Generator {
	return NewGeneratorWithReader(rand.Reader, cacheSize, minDec, minChar, minSpec)
}

// NewGeneratorWithReader uses the supplied reader as its entropy source.
// Use NewGenerator when passwords are not deterministic fixtures.
func NewGeneratorWithReader(reader io.Reader, cacheSize, minDec, minChar, minSpec int) *Generator {
	if reader == nil {
		reader = rand.Reader
	}
	generator := &Generator{
		reader:       reader,
		passwords:    make([]string, 0),
		maxCacheSize: cacheSize,
		minDec:       minDec,
		minChar:      minChar,
		minSpec:      minSpec,
	}

	passwords := make([]string, cacheSize)
	for i := 0; i < cacheSize; i++ {
		passwords[i] = generator.genPass()
	}
	generator.passwords = passwords

	return generator
}

func (g *Generator) GetPassword() string {
	g.mtx.Lock()
	g.queries++
	defer g.mtx.Unlock()
	if g.queries > g.maxCacheSize {
		passwords := make([]string, g.maxCacheSize)
		for i := 0; i < g.maxCacheSize; i++ {
			passwords[i] = g.genPass()
		}
		g.passwords = passwords
		g.queries = 1
	}

	return g.passwords[g.queries-1]
}

func (g *Generator) genPass() string {
	builder := strings.Builder{}
	for i := 0; i < g.minChar; i++ {
		builder.WriteRune(g.randChar())
	}

	for i := 0; i < g.minDec; i++ {
		builder.WriteRune(g.randDec())
	}

	for i := 0; i < g.minSpec; i++ {
		builder.WriteRune(g.randSpec())
	}

	return g.shuffleString(builder.String())
}

func (g *Generator) randChar() rune {
	c, _ := rand.Int(g.reader, big.NewInt(math.MaxInt64))
	if c.Int64()%2 == 0 {
		return g.randCharLowercase()
	} else {
		return g.randCharUppercase()
	}
}

func (g *Generator) randCharLowercase() rune {
	c, _ := rand.Int(g.reader, big.NewInt(26))
	return rune("abcdefghijklmnopqrstuvwxyz"[c.Int64()])
}

func (g *Generator) randCharUppercase() rune {
	c, _ := rand.Int(g.reader, big.NewInt(26))
	return rune("ABCDEFGHIJKLMNOPQRSTUVWXYZ"[c.Int64()])
}

func (g *Generator) randDec() rune {
	c, _ := rand.Int(g.reader, big.NewInt(10))
	return rune("0123456789"[c.Int64()])
}

func (g *Generator) randSpec() rune {
	ind, _ := rand.Int(g.reader, big.NewInt(int64(len(SpecialChars))))
	return rune(SpecialChars[ind.Int64()])
}

func (g *Generator) shuffleString(str string) string {
	shuffled := []rune(str)
	for i := len(shuffled) - 1; i > 0; i-- {
		j, err := g.cryptoIndex(i + 1)
		if err != nil {
			return str
		}
		shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
	}

	return string(shuffled)
}

func (g *Generator) cryptoIndex(max int) (int, error) {
	if max <= 0 {
		return 0, fmt.Errorf("max should be positive")
	}
	index, err := rand.Int(g.reader, big.NewInt(int64(max)))
	if err != nil {
		return 0, err
	}
	return int(index.Int64()), nil
}
