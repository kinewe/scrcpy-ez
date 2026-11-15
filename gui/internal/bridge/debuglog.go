package bridge

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"
)

const debugLogQueueCap = 4096

var (
	debugMu      sync.Mutex
	debugSendMu  sync.RWMutex // protects channel send versus close
	debugEnabled atomic.Bool
	debugPath    string
	debugChPtr   atomic.Pointer[chan string]
	debugDone    chan struct{}
	debugDropped atomic.Int64
)

// EnableDebugLogFromEnv is called by the GUI, which captures the child output.
func EnableDebugLogFromEnv() {
	switch path := os.Getenv("SCEZ_BRIDGE_LOG"); path {
	case "":
		return
	case "1":
		exe, _ := os.Executable()
		enableDebugLog(filepath.Join(filepath.Dir(exe), "bridge.log"), true)
	default:
		EnableDebugLog(path)
	}
}

// EnableDebugLog starts an asynchronous writer for the given path.
func EnableDebugLog(path string) {
	enableDebugLog(path, false)
}

func enableDebugLog(path string, cleanLegacy bool) {
	debugMu.Lock()
	defer debugMu.Unlock()
	if debugEnabled.Load() {
		return
	}

	writer := rotatingDebugLog{path: path, limit: debugLogFileLimit}
	if err := writer.open(); err != nil {
		return
	}
	debugPath = path
	ch := make(chan string, debugLogQueueCap)
	done := make(chan struct{})
	debugChPtr.Store(&ch)
	debugDone = done
	debugDropped.Store(0)
	debugEnabled.Store(true)

	go func() {
		defer close(done)
		defer writer.close()
		if err := writer.prune(time.Now(), cleanLegacy); err != nil {
			writer.close()
		}
		for line := range ch {
			if writer.write(line) != nil {
				debugDropped.Add(1)
			}
		}
	}()
}

// DisableDebugLog stops accepting new lines, drains the queue and closes the file.
func DisableDebugLog() {
	debugMu.Lock()
	defer debugMu.Unlock()
	if !debugEnabled.Load() {
		return
	}

	debugSendMu.Lock()
	debugEnabled.Store(false)
	old := debugChPtr.Swap(nil)
	close(*old)
	debugSendMu.Unlock()

	<-debugDone
	debugPath = ""
	debugDone = nil
}

func FlushDebugLog() {
	DisableDebugLog()
}

// DebugLog never writes to disk on the caller's thread.
func DebugLog(format string, args ...any) {
	if !debugEnabled.Load() {
		return
	}
	line := time.Now().Format("15:04:05.000 ") + fmt.Sprintf(format, args...) + "\n"
	debugSendMu.RLock()
	defer debugSendMu.RUnlock()
	ch := debugChPtr.Load()
	if ch == nil {
		return
	}
	select {
	case *ch <- line:
	default:
		debugDropped.Add(1)
	}
}

func debugLogDroppedN() int64 {
	return debugDropped.Load()
}

func debugLogPathForTest() string {
	debugMu.Lock()
	defer debugMu.Unlock()
	return debugPath
}
