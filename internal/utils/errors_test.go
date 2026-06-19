package utils

import (
	"errors"
	"fmt"
	"os"
	"testing"
)

func TestWrapError(t *testing.T) {
	// Test nil error
	if err := WrapError(nil, "context"); err != nil {
		t.Fatalf("WrapError(nil) should return nil, got: %v", err)
	}

	// Test wrapping non-nil error
	baseErr := errors.New("base error")
	wrapped := WrapError(baseErr, "context: %w")
	if wrapped == nil {
		t.Fatal("WrapError should return non-nil error")
	}
	if !errors.Is(wrapped, baseErr) {
		t.Fatal("Wrapped error should contain base error")
	}
}

func TestLogAndReturn(t *testing.T) {
	// Test with nil error
	err := LogAndReturn(nil, "test message")
	if err != nil {
		t.Fatalf("LogAndReturn(nil) should return nil, got: %v", err)
	}

	// Test with non-nil error
	baseErr := errors.New("test error")
	err = LogAndReturn(baseErr, "test message with arg: %s", "arg")
	if err != baseErr {
		t.Errorf("LogAndReturn should return original error, got: %v", err)
	}
}

func TestLogAndContinue(t *testing.T) {
	// These just log and don't return, so we can't easily test the logging
	// Just ensure they don't panic
	LogAndContinue(nil, "test")
	LogAndContinue(errors.New("error"), "test with arg: %s", "arg")
}

func TestErrorChain(t *testing.T) {
	// Test nil error
	if chain := ErrorChain(nil); chain != "" {
		t.Errorf("ErrorChain(nil) should return empty string, got: %q", chain)
	}

	// Test single error
	singleErr := errors.New("single error")
	chain := ErrorChain(singleErr)
	if chain != "single error" {
		t.Errorf("ErrorChain(single) = %q, want %q", chain, "single error")
	}

	// Test wrapped error
	base := errors.New("base")
	wrapped := fmt.Errorf("wrapped: %w", base)
	chain = ErrorChain(wrapped)
	if !contains(chain, "wrapped") || !contains(chain, "base") {
		t.Errorf("ErrorChain(wrapped) = %q, should contain both 'wrapped' and 'base'", chain)
	}
}

func TestUnwrapAll(t *testing.T) {
	// Test nil error
	if unwrapped := UnwrapAll(nil); len(unwrapped) != 0 {
		t.Errorf("UnwrapAll(nil) should return empty slice, got: %v", unwrapped)
	}

	// Test single error
	single := errors.New("single")
	unwrapped := UnwrapAll(single)
	if len(unwrapped) != 1 || unwrapped[0] != single {
		t.Errorf("UnwrapAll(single) should return [single], got: %v", unwrapped)
	}

	// Test wrapped errors
	base := errors.New("base")
	middle := fmt.Errorf("middle: %w", base)
	wrapped := fmt.Errorf("wrapped: %w", middle)
	unwrapped = UnwrapAll(wrapped)
	if len(unwrapped) != 3 {
		t.Errorf("UnwrapAll(3-level) should return 3 errors, got: %d", len(unwrapped))
	}
}

func TestUnwrap(t *testing.T) {
	// Test standard error (no Unwrap method)
	simple := errors.New("simple")
	if unwrapped := Unwrap(simple); unwrapped != nil {
		t.Errorf("Unwrap(simple error) should return nil, got: %v", unwrapped)
	}

	// Test wrapped error
	base := errors.New("base")
	wrapped := fmt.Errorf("wrapped: %w", base)
	unwrapped := Unwrap(wrapped)
	if unwrapped != base {
		t.Errorf("Unwrap(wrapped) should return base, got: %v", unwrapped)
	}
}

func TestIsFatal(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"not exist", os.ErrNotExist, false},
		{"permission", os.ErrPermission, true},
		{"other", errors.New("some error"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsFatal(tt.err)
			if got != tt.want {
				t.Errorf("IsFatal(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsAt(s, substr, 0))
}

func containsAt(s, substr string, start int) bool {
	for i := start; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
