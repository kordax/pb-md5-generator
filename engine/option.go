package engine

import (
	"github.com/kordax/pb-md5-generator/internal/parser"
)

type Option[T any] = parser.Option[T]

func Some[T any](value T) Option[T] {
	return parser.Some(value)
}

func OptionFromPtr[T any](value *T) Option[T] {
	return parser.OptionFromPtr(value)
}

type Pair[L, R any] = parser.Pair[L, R]
