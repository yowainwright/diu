package daemon

import (
	"context"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/yowainwright/diu/internal/core"
	"github.com/yowainwright/diu/internal/monitors"
	"github.com/yowainwright/diu/internal/storage"
)

type socketControlRequest struct {
	Type string `json:"type"`
}

type socketControlResponse struct {
	Status string `json:"status"`
	PID    int    `json:"pid"`
}

type Daemon struct {
	config              *core.Config
	storage             storage.Storage
	registry            *monitors.MonitorRegistry
	eventChan           chan *core.ExecutionRecord
	admissionMu         sync.RWMutex
	httpServer          *http.Server
	socketListener      net.Listener
	socketInfo          os.FileInfo
	socketSlots         chan struct{}
	ctx                 context.Context
	cancel              context.CancelFunc
	wg                  sync.WaitGroup
	startTime           time.Time
	stopOnce            sync.Once
	stopErr             error
	stopped             atomic.Bool
	logger              *log.Logger
	logSink             io.Closer
	startupWarnings     []string
	pidFile             *os.File
	hasPIDFileOwnership bool
	hasSocketOwnership  bool
	refreshInventory    func(context.Context) error
}
