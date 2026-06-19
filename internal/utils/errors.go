// Package utils provides common utility functions for error handling.
package utils

import (
	"fmt"
	"log"
	"os"
	"strings"
)

// LogAndReturn logs an error and returns it.
// Use this for non-fatal errors where you want to log but continue processing.
func LogAndReturn(err error, format string, args ...interface{}) error {
	if err != nil {
		log.Printf(format+": %v", append(args, err)...)
	}
	return err
}

// LogAndContinue logs an error but continues execution (returns nil).
// Use this for warnings or non-critical errors.
func LogAndContinue(err error, format string, args ...interface{}) {
	if err != nil {
		log.Printf(format+": %v", append(args, err)...)
	}
}

// WrapError wraps an error with context if it's not nil.
// Returns nil if the error is nil, otherwise returns fmt.Errorf(format, args...).
func WrapError(err error, format string, args ...interface{}) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf(format+": %w", append(args, err)...)
}

// ExitOnError logs an error and exits the program if err is not nil.
// Use this for fatal errors that cannot be recovered from.
func ExitOnError(err error, message string, args ...interface{}) {
	if err != nil {
		if len(args) > 0 {
			log.Fatalf(message+": %v", append(args, err)...)
		} else {
			log.Fatalf("%s: %v", message, err)
		}
	}
}

// ExitWithMessage logs a message and exits.
func ExitWithMessage(message string, args ...interface{}) {
	if len(args) > 0 {
		log.Fatalf(message, args...)
	} else {
		log.Fatal(message)
	}
}

// ErrorChain returns a combined error message including all errors in the chain.
func ErrorChain(err error) string {
	if err == nil {
		return ""
	}

	var builder strings.Builder
	builder.WriteString(err.Error())

	for _, e := range UnwrapAll(err) {
		if e != err {
			builder.WriteString(": ")
			builder.WriteString(e.Error())
		}
	}

	return builder.String()
}

// UnwrapAll returns all errors in the error chain.
func UnwrapAll(err error) []error {
	if err == nil {
		return nil
	}

	var errors []error
	current := err
	for current != nil {
		errors = append(errors, current)
		current = Unwrap(current)
	}
	return errors
}

// Unwrap returns the underlying error.
func Unwrap(err error) error {
	type unwrapper interface {
		Unwrap() error
	}
	if u, ok := err.(unwrapper); ok {
		return u.Unwrap()
	}
	return nil
}

// IsFatal returns true if the error should cause the program to exit.
func IsFatal(err error) bool {
	if err == nil {
		return false
	}

	// Check for specific fatal errors
	if os.IsNotExist(err) {
		return false // File not found is usually not fatal
	}
	if os.IsPermission(err) {
		return true // Permission errors are usually fatal
	}

	// Check error type
	_, isPathError := err.(*os.PathError)
	if isPathError {
		return false
	}

	return false
}

// Silently removes a file, ignoring errors.
func SilentRemove(path string) {
	_ = os.Remove(path)
}

// Silently removes a directory and all its contents, ignoring errors.
func SilentRemoveAll(path string) {
	_ = os.RemoveAll(path)
}
