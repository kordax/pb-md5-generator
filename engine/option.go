package engine

type Option[T any] struct {
	value   T
	present bool
}

func Some[T any](value T) Option[T] {
	return Option[T]{value: value, present: true}
}

func OptionFromPtr[T any](value *T) Option[T] {
	if value == nil {
		return Option[T]{}
	}
	return Some(*value)
}

func (o Option[T]) Present() bool {
	return o.present
}

func (o Option[T]) Get() *T {
	if !o.present {
		return nil
	}
	return &o.value
}

func (o Option[T]) OrElse(fallback T) T {
	if !o.present {
		return fallback
	}
	return o.value
}

func (o Option[T]) IfPresent(fn func(T)) {
	if o.present {
		fn(o.value)
	}
}

type Pair[L, R any] struct {
	Left  L
	Right R
}
