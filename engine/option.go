package engine

import (
	"github.com/kordax/basic-utils/v3/uopt"
	"github.com/kordax/basic-utils/v3/upair"
)

type Option[T any] = uopt.Opt[T]

func Some[T any](value T) Option[T] {
	return uopt.Of(value)
}

func OptionFromPtr[T any](value *T) Option[T] {
	return uopt.OfNullable(value)
}

type Pair[L, R any] = upair.Pair[L, R]
