package lazy

import (
	"errors"
	"testing"
)

func TestMap_Get(t *testing.T) {
	t.Run("computes and caches value", func(t *testing.T) {
		callCount := 0
		m := &Map[string, int]{}

		computeValue := func(key string) int {
			callCount++
			return len(key)
		}

		// First call should compute the value
		got := m.Get("hello", computeValue)

		const want = 5
		if got != want {
			t.Errorf("got value %d, want %d", got, want)
		}

		if callCount != 1 {
			t.Errorf("got call count %d, want 1", callCount)
		}

		// Second call should return cached value
		got = m.Get("hello", computeValue)

		if got != want {
			t.Errorf("got value %d, want %d", got, want)
		}

		if callCount != 1 {
			t.Errorf("got call count %d, want 1 (function should not be called again)", callCount)
		}
	})

	t.Run("different keys compute separate values", func(t *testing.T) {
		m := &Map[string, int]{}

		computeValue := func(key string) int {
			return len(key)
		}

		// Get value for first key
		got1 := m.Get("hello", computeValue)

		const want1 = 5
		if got1 != want1 {
			t.Errorf("got value for key1 %d, want %d", got1, want1)
		}

		// Get value for second key
		got2 := m.Get("world!", computeValue)

		const want2 = 6
		if got2 != want2 {
			t.Errorf("got value for key2 %d, want %d", got2, want2)
		}
	})
}

func TestMap_GetErr(t *testing.T) {
	t.Run("computes and caches value and error", func(t *testing.T) {
		callCount := 0
		m := &Map[string, int]{}

		computeValue := func(key string) (int, error) {
			callCount++
			if key == "error" {
				return 0, errors.New("test error")
			}
			return len(key), nil
		}

		// First call should compute the value
		got, err := m.GetErr("hello", computeValue)

		const want = 5
		if got != want {
			t.Errorf("got value %d, want %d", got, want)
		}

		if err != nil {
			t.Errorf("got error %v, want nil", err)
		}

		if callCount != 1 {
			t.Errorf("got call count %d, want 1", callCount)
		}

		// Second call should return cached value
		got, err = m.GetErr("hello", computeValue)

		if got != want {
			t.Errorf("got value %d, want %d", got, want)
		}

		if err != nil {
			t.Errorf("got error %v, want nil", err)
		}

		if callCount != 1 {
			t.Errorf("got call count %d, want 1 (function should not be called again)", callCount)
		}
	})

	t.Run("handles and caches errors", func(t *testing.T) {
		callCount := 0
		m := &Map[string, int]{}

		testError := errors.New("test error")
		computeValue := func(key string) (int, error) {
			callCount++
			return 0, testError
		}

		// First call should compute the value and error
		got, err := m.GetErr("error", computeValue)
		want := 0

		if got != want {
			t.Errorf("got value %d, want %d", got, want)
		}

		if err == nil {
			t.Errorf("got error nil, want non-nil error")
		} else if !errors.Is(err, testError) {
			t.Errorf("got error %v, want %v", err, testError)
		}

		if callCount != 1 {
			t.Errorf("got call count %d, want 1", callCount)
		}

		// Second call should return cached value and error
		got, err = m.GetErr("error", computeValue)

		if got != want {
			t.Errorf("got value %d, want %d", got, want)
		}

		if err == nil {
			t.Errorf("got error nil, want non-nil error")
		} else if !errors.Is(err, testError) {
			t.Errorf("got error %v, want %v", err, testError)
		}

		if callCount != 1 {
			t.Errorf("got call count %d, want 1 (function should not be called again)", callCount)
		}
	})
}
