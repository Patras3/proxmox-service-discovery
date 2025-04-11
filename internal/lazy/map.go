package lazy

// Map represents a generic map with a lazy evaluation of its values. The zero
// value is valid and can be used.
//
// This implementation is not thread-safe.
type Map[K comparable, V any] struct {
	// m is the underlying map that stores the values.
	//
	// This map will always contain a value, even if an error is
	// encoutered.
	m map[K]V
	// errs stores any errors encountered during value computation.
	//
	// This map will only contain an error if one occurs during value
	// computation.
	errs map[K]error
}

// Get returns the value associated with the key, computing it if necessary using
// the provided function. If the value has already been computed, the function
// is not called and the cached value is returned.
func (m *Map[K, V]) Get(key K, fn func(K) V) V {
	if value, ok := m.m[key]; ok {
		return value
	}
	value := fn(key)

	if m.m == nil {
		m.m = make(map[K]V)
	}
	m.m[key] = value
	return value
}

// GetErr returns the value and any error associated with the key, computing
// them if necessary using the provided function. If the value has already been
// computed, the function is not called and the cached value and error are
// returned.
func (m *Map[K, V]) GetErr(key K, fn func(K) (V, error)) (V, error) {
	if value, ok := m.m[key]; ok {
		// on error, value is zero; return both here
		return value, m.errs[key]
	}

	if m.m == nil {
		m.m = make(map[K]V)
	}
	value, err := fn(key)
	m.m[key] = value

	// Save a bit of memory by not storing nil errors
	if err != nil {
		if m.errs == nil {
			m.errs = make(map[K]error)
		}
		m.errs[key] = err
	}
	return value, err
}
