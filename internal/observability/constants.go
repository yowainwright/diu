package observability

const (
	LogFileName          = "diu.log"
	previousLogFileName  = "diu.log.previous"
	fallbackMarkerName   = "fallback-contention"
	maxLocalLogBytes     = 4 << 20
	maxDiagnosticLogSize = 128 << 10
)
