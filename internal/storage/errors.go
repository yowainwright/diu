package storage

import "errors"

var (
	ErrExecutionLogNotFound          = errors.New("execution log not found")
	ErrUnsupportedExecutionLogFormat = errors.New("unsupported execution log format")
	ErrEmptyPath                     = errors.New("path cannot be empty")
	ErrNilExecutionRecord            = errors.New("execution record cannot be nil")
	ErrNilPackage                    = errors.New("package cannot be nil")
)
