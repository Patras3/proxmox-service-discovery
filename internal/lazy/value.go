// Package lazy provides lazy evaluation primitives.
package lazy

// Value represents a lazily evaluated value of type T.
// The value is computed only once, on the first access.
//
// This implementation is not thread-safe.
type Value[T any] struct {
	value T
	err   error
	done  bool
}

// Get returns the value, computing it if necessary using the provided function.
// If the value has already been computed, the function is not called and the
// cached value is returned.
func (v *Value[T]) Get(fn func() T) T {
	if !v.done {
		v.value = fn()
		v.done = true
	}
	return v.value
}

// GetErr returns the value and any error, computing them if necessary using
// the provided function. If the value has already been computed, the function
// is not called and the cached value and error are returned.
func (v *Value[T]) GetErr(fn func() (T, error)) (T, error) {
	if !v.done {
		v.value, v.err = fn()
		v.done = true
	}
	return v.value, v.err
}
