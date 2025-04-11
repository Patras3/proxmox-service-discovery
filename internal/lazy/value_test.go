package lazy

import (
	"errors"
	"testing"
)

func TestValue_Get(t *testing.T) {
	t.Run("computes and caches value", func(t *testing.T) {
		callCount := 0
		var v Value[int]

		computeValue := func() int {
			callCount++
			return 42
		}

		// First call should compute the value
		got := v.Get(computeValue)

		const want = 42
		if got != want {
			t.Errorf("got value %d, want %d", got, want)
		}

		if callCount != 1 {
			t.Errorf("got call count %d, want 1", callCount)
		}

		// Second call should return cached value
		got = v.Get(computeValue)

		if got != want {
			t.Errorf("got value %d, want %d", got, want)
		}

		if callCount != 1 {
			t.Errorf("got call count %d, want 1 (function should not be called again)", callCount)
		}
	})

	t.Run("zero value works correctly", func(t *testing.T) {
		var v Value[string]

		got := v.Get(func() string {
			return "test"
		})
		want := "test"

		if got != want {
			t.Errorf("got value %q, want %q", got, want)
		}
	})
}

func TestValue_GetErr(t *testing.T) {
	t.Run("computes and caches value and nil error", func(t *testing.T) {
		callCount := 0
		var v Value[int]

		computeValue := func() (int, error) {
			callCount++
			return 42, nil
		}

		// First call should compute the value
		got, err := v.GetErr(computeValue)

		const want = 42
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
		got, err = v.GetErr(computeValue)

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

	t.Run("computes and caches value and error", func(t *testing.T) {
		callCount := 0
		var v Value[int]

		testErr := errors.New("test error")
		computeValue := func() (int, error) {
			callCount++
			return 0, testErr
		}

		// First call should compute the value and error
		got, err := v.GetErr(computeValue)

		const want = 0
		if got != want {
			t.Errorf("got value %d, want %d", got, want)
		}

		if !errors.Is(err, testErr) {
			t.Errorf("got error %v, want %v", err, testErr)
		}

		if callCount != 1 {
			t.Errorf("got call count %d, want 1", callCount)
		}

		// Second call should return cached value and error
		got, err = v.GetErr(computeValue)

		if got != want {
			t.Errorf("got value %d, want %d", got, want)
		}

		if !errors.Is(err, testErr) {
			t.Errorf("got error %v, want %v", err, testErr)
		}

		if callCount != 1 {
			t.Errorf("got call count %d, want 1 (function should not be called again)", callCount)
		}
	})
}
