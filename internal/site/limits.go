package site

import (
	"bytes"
	"fmt"
)

// checkMarkupBudget bounds potential AST expansion before allocating the AST.
// It is deliberately conservative: structural punctuation inside code also
// counts. Byte limits alone permit millions of tiny markup nodes.
func checkMarkupBudget(source []byte) error {
	units := 0
	for _, value := range source {
		switch value {
		case '\n', '\r', '#', '*', '_', '~', '`', '[', ']', '(', ')', '<', '>', '|', '!', '\\', '&', ':', '-', '+', '=', '.', '/', '@':
			units++
			if units > maxMarkupUnits {
				return fmt.Errorf("markdown complexity exceeds %d structural punctuation characters and line breaks; split the page", maxMarkupUnits)
			}
		}
	}
	return nil
}

type boundedBuffer struct {
	buffer bytes.Buffer
	limit  int
}

func (b *boundedBuffer) Write(value []byte) (int, error) {
	if len(value) > b.limit-b.buffer.Len() {
		return 0, fmt.Errorf("rendered output exceeds %d bytes", b.limit)
	}
	return b.buffer.Write(value)
}

func (b *boundedBuffer) WriteString(value string) (int, error) {
	if len(value) > b.limit-b.buffer.Len() {
		return 0, fmt.Errorf("rendered output exceeds %d bytes", b.limit)
	}
	return b.buffer.WriteString(value)
}

func (b *boundedBuffer) String() string { return b.buffer.String() }
func (b *boundedBuffer) Bytes() []byte  { return b.buffer.Bytes() }
