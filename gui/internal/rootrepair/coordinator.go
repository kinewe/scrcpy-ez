package rootrepair

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Request struct {
	Serial   string `json:"serial"`
	Identity string `json:"identity"`
	Key      string `json:"key"`
}
type Outcome struct {
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}
type flight struct {
	done     chan struct{}
	cancel   context.CancelFunc
	waiters  int
	outcome  Outcome
	finished time.Time
}

// Coordinator is used only after a confirmed failed server upload. Normal casts
// never call it. Consent is persistent; denial suppression and flights are not.
type Coordinator struct {
	mu      sync.Mutex
	path    string
	enabled map[string]bool
	held    map[string]bool
	flights map[string]*flight
	run     func(context.Context, Request) (Report, error)
}

func NewCoordinator(path string, run func(context.Context, Request) (Report, error)) *Coordinator {
	c := &Coordinator{path: path, enabled: map[string]bool{}, held: map[string]bool{}, flights: map[string]*flight{}, run: run}
	if b, e := os.ReadFile(path); e == nil {
		var saved map[string]bool
		if json.Unmarshal(b, &saved) == nil && saved != nil {
			c.enabled = saved
		}
	}
	return c
}

func (c *Coordinator) SetEnabled(identity string, enabled bool) error {
	if !safeID.MatchString(identity) || strings.HasPrefix(identity, "-") {
		return errors.New("设备身份无效")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	next := map[string]bool{}
	for k, v := range c.enabled {
		next[k] = v
	}
	if enabled {
		next[identity] = true
	} else {
		delete(next, identity)
	}
	if c.path != "" {
		if e := os.MkdirAll(filepath.Dir(c.path), 0700); e != nil {
			return e
		}
		b, _ := json.MarshalIndent(next, "", "  ")
		f, e := os.CreateTemp(filepath.Dir(c.path), "root-preference-*")
		if e != nil {
			return e
		}
		name := f.Name()
		defer os.Remove(name)
		_, e = f.Write(b)
		ce := f.Close()
		if e == nil {
			e = ce
		}
		if e == nil {
			e = os.Rename(name, c.path)
		}
		if e != nil {
			return e
		}
	}
	c.enabled = next
	delete(c.held, identity)
	if f := c.flights[identity]; f != nil {
		if !enabled {
			f.cancel()
		}
		// A running flight must finish before another can mutate the same device.
		if !f.finished.IsZero() {
			delete(c.flights, identity)
		}
	}
	return nil
}

func (c *Coordinator) Repair(ctx context.Context, req Request) Outcome {
	if !safeID.MatchString(req.Serial) || !safeID.MatchString(req.Identity) || strings.HasPrefix(req.Serial, "-") || req.Key == "" {
		return Outcome{Status: "failed", Error: "缺少安全的设备和连接标识"}
	}
	c.mu.Lock()
	if !c.enabled[req.Identity] {
		c.mu.Unlock()
		return Outcome{Status: "disabled"}
	}
	if c.held[req.Identity] {
		c.mu.Unlock()
		return Outcome{Status: "held", Error: "上次修复未通过，请手动再次启用修复"}
	}
	f := c.flights[req.Identity]
	if f != nil && !f.finished.IsZero() && (f.outcome.Status == "canceled" || time.Since(f.finished) > 5*time.Second) {
		delete(c.flights, req.Identity)
		f = nil
	}
	if f == nil {
		work, cancel := context.WithTimeout(context.WithoutCancel(ctx), Budget)
		f = &flight{done: make(chan struct{}), cancel: cancel}
		c.flights[req.Identity] = f
		go func() {
			defer cancel()
			r, e := c.run(work, req)
			result := Outcome{Status: r.Status}
			if e != nil {
				result = Outcome{Status: "failed", Error: e.Error()}
				if errors.Is(e, context.Canceled) {
					result.Status = "canceled"
				}
			}
			c.mu.Lock()
			f.outcome = result
			f.finished = time.Now()
			// Cancellation does not imply denial; retry still requires a new user intent
			// in the worker. Other failures are suppressed across USB/WiFi until consent.
			if e != nil && !errors.Is(e, context.Canceled) {
				c.held[req.Identity] = true
			}
			close(f.done)
			c.mu.Unlock()
		}()
	}
	f.waiters++
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		f.waiters--
		drain := f.waiters == 0 && f.finished.IsZero()
		if drain {
			f.cancel()
		}
		c.mu.Unlock()
		if drain {
			// Reap canceled ADB before the standalone worker exits.
			select {
			case <-f.done:
			case <-time.After(2 * time.Second):
			}
		}
	}()
	select {
	case <-ctx.Done():
		return Outcome{Status: "canceled", Error: ctx.Err().Error()}
	case <-f.done:
		return f.outcome
	}
}

func (o Outcome) Accepted() bool { return o.Status == "healthy" || o.Status == "repaired" }

func (c *Coordinator) Enabled(identity string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.enabled[identity]
}
func (o Outcome) String() string {
	switch o.Status {
	case "disabled":
		return "此设备尚未启用 root 修复"
	case "held":
		return "上次授权或修复未通过，自动修复已暂停"
	case "canceled":
		return "操作取消或连接变化，修复已停止"
	default:
		return fmt.Sprintf("修复未通过：%s", o.Error)
	}
}
