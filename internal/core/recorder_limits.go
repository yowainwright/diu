package core

import (
	"fmt"
	"path/filepath"
	"time"
)

const (
	MaxRecorderWorkers      = 4
	RecorderTimeout         = 2 * time.Second
	MaxRecorderPayloadBytes = 1024 * 1024
)

func RecorderSlotPath(dataDir string, slot int) string {
	name := fmt.Sprintf("recorder-%d.lock", slot)
	return filepath.Join(dataDir, name)
}
