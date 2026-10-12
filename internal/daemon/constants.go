package daemon

import (
	"time"
)

const (
	maxExecutionRecordBodyBytes = 1 << 20
	maxRecordedCommandLength    = 4096
	socketControlTimeout        = time.Second
	socketRequestPing           = "ping"
	socketRequestStop           = "stop"
	socketStatusOK              = "ok"
	maxEventBatchSize           = 64
	maxSocketHandlers           = 16
	eventAdmissionTimeout       = time.Second
	defaultExecutionQueryLimit  = 100
)
