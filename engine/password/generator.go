package password

import (
	"crypto/rand"
	"math"
	"math/big"
	mrand "math/rand"
	"strings"
	"sync"
	"time"
)

const SpecialChars = "!@#$%^&*()[]"

type Generator struct {
	passwords    []string
	queries      int
	maxCacheSize int

	minDec  int
	minChar int
	minSpec int

	mtx sync.Mutex
}

func NewGenerator(cacheSize, minDec, minChar, minSpec int) *Generator {
	generator := &Generator{
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

	return shuffleString(builder.String())
}

func (g *Generator) randChar() rune {
	c, _ := rand.Int(rand.Reader, big.NewInt(math.MaxInt64))
	if c.Int64()%2 == 0 {
		return g.randCharLowercase()
	} else {
		return g.randCharUppercase()
	}
}

func (g *Generator) randCharLowercase() rune {
	c, _ := rand.Int(rand.Reader, big.NewInt(26))
	return 'a' + rune(c.Int64())
}

func (g *Generator) randCharUppercase() rune {
	c, _ := rand.Int(rand.Reader, big.NewInt(26))
	return 'A' + rune(c.Int64())
}

func (g *Generator) randDec() rune {
	c, _ := rand.Int(rand.Reader, big.NewInt(10))
	return '0' + rune(c.Int64())
}

func (g *Generator) randSpec() rune {
	ind, _ := rand.Int(rand.Reader, big.NewInt(int64(len(SpecialChars))))
	return rune(SpecialChars[ind.Int64()])
}

func shuffleString(str string) string {
	shuffled := make([]rune, len(str))
	mrand.New(mrand.NewSource(time.Now().UTC().UnixNano() * (mrand.Int63() + 1)))
	perm := mrand.Perm(len(str))

	for i, v := range perm {
		shuffled[v] = rune(str[i])
	}

	return string(shuffled)
}
