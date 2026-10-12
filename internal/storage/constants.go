package storage

import "time"

const (
	maxBackupPathAttempts = 1000
	executionLogFormat    = "ndjson-v1"
	storageLockRetryDelay = 10 * time.Millisecond
)

const (
	compactionHeadroomDivisor = 10
	byteHeadroomThreshold     = 1 << 20
	recordHeadroomThreshold   = 1000
)

const storageCommitJournalSuffix = ".commit"
