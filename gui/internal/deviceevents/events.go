// Package deviceevents publishes raw ADB transport snapshots without property queries.
package deviceevents

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

type Transport struct {
	Serial     string `json:"serial"`
	State      string `json:"state"`
	Kind       string `json:"kind"`
	ID         string `json:"id,omitempty"`
	Model      string `json:"model,omitempty"`
	Generation uint64 `json:"generation"`
}

type Snapshot struct {
	Epoch      uint64      `json:"epoch"`
	Sequence   uint64      `json:"sequence"`
	Available  bool        `json:"available"`
	Transports []Transport `json:"transports"`
	Error      string      `json:"error,omitempty"`
	Learning   []string    `json:"learning,omitempty"`
}

type Hub struct {
	mu               sync.Mutex
	last             Snapshot
	subs             map[chan Snapshot]struct{}
	versions         map[string]uint64
	previous         map[string]Transport
	holdUSB          bool
	holds            map[string]uint64
	explicitLearning map[string]bool
}

func NewLearningHub() *Hub { h := NewHub(); h.holdUSB = true; return h }

func NewHub() *Hub {
	return &Hub{subs: map[chan Snapshot]struct{}{}, versions: map[string]uint64{}, previous: map[string]Transport{}, holds: map[string]uint64{}, explicitLearning: map[string]bool{}}
}

// Subscribe atomically registers the receiver and supplies the current baseline.
// Slow readers get the latest full snapshot, including transport generations.
func (h *Hub) Subscribe(ctx context.Context) <-chan Snapshot {
	c := make(chan Snapshot, 64)
	h.mu.Lock()
	h.subs[c] = struct{}{}
	c <- clone(h.last)
	h.mu.Unlock()
	go func() {
		<-ctx.Done()
		h.mu.Lock()
		delete(h.subs, c)
		close(c)
		h.mu.Unlock()
	}()
	return c
}

func clone(s Snapshot) Snapshot {
	s.Transports = append([]Transport(nil), s.Transports...)
	s.Learning = append([]string(nil), s.Learning...)
	return s
}

func (h *Hub) Current() Snapshot { h.mu.Lock(); defer h.mu.Unlock(); return clone(h.last) }

// SameTransports checks the raw connection token, independent of learning
// notifications or transport ordering. A removed/re-added serial has a new
// generation even when its final state and name are unchanged.
func SameTransports(a, b Snapshot) bool {
	if a.Epoch != b.Epoch || a.Available != b.Available || len(a.Transports) != len(b.Transports) {
		return false
	}
	idx := make(map[string]Transport, len(a.Transports))
	for _, t := range a.Transports {
		idx[t.Serial] = t
	}
	for _, t := range b.Transports {
		if old, ok := idx[t.Serial]; !ok || old != t {
			return false
		}
	}
	return true
}

// SetLearning shields a USB transport while GUI learning may restart adbd.
func (h *Hub) SetLearning(serial string, active bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	s := clone(h.last)
	h.holds[serial]++
	if active {
		h.explicitLearning[serial] = true
	} else {
		delete(h.explicitLearning, serial)
	}
	var next []string
	for _, v := range s.Learning {
		if v != serial {
			next = append(next, v)
		}
	}
	if active {
		next = append(next, serial)
	}
	s.Learning = next
	h.publishLocked(s, true, true)
}

func (h *Hub) Publish(s Snapshot) {
	h.publish(s, false, false)
}

func (h *Hub) publish(s Snapshot, remote, learningUpdate bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.publishLocked(s, remote, learningUpdate)
}

func (h *Hub) publishLocked(s Snapshot, remote, learningUpdate bool) {
	s = clone(s)
	if !remote && !learningUpdate {
		s.Learning = append([]string(nil), h.last.Learning...)
	}
	if s.Available {
		cur := map[string]Transport{}
		for i, t := range s.Transports {
			old, exists := h.previous[t.Serial]
			if h.holdUSB && !remote && !learningUpdate && t.Kind == "usb" && t.State == "device" && (!exists || old.State != "device") {
				found := false
				for _, v := range s.Learning {
					if v == t.Serial {
						found = true
					}
				}
				if !found {
					s.Learning = append(s.Learning, t.Serial)
					// A bounded event-triggered shield, including if UI enrichment fails.
					serial := t.Serial
					h.holds[serial]++
					version := h.holds[serial]
					time.AfterFunc(15*time.Second, func() { h.expireHold(serial, version) })
				}
			}
			if remote {
				h.versions[t.Serial] = t.Generation
			} else if !exists || old.State != t.State || old.Kind != t.Kind || old.ID != t.ID || h.last.Epoch != s.Epoch {
				h.versions[t.Serial]++
			}
			t.Generation = h.versions[t.Serial]
			s.Transports[i] = t
			cur[t.Serial] = t
		}
		h.previous = cur
		if !remote && !learningUpdate {
			// An automatic arrival hold is only a hand-off to the GUI learner.
			// If USB disappears before the learner starts, no tcpip command could
			// have caused an adbd restart and there is nothing left to protect.
			var next []string
			for _, serial := range s.Learning {
				if _, present := cur[serial]; present || h.explicitLearning[serial] {
					next = append(next, serial)
				} else {
					h.holds[serial]++
				}
			}
			s.Learning = next
		}
	}
	h.last = clone(s)
	for c := range h.subs {
		select {
		case c <- clone(s):
		default:
			select {
			case <-c:
			default:
			}
			c <- clone(s)
		}
	}
}

func (h *Hub) expireHold(serial string, version uint64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.holds[serial] != version {
		return
	}
	s := clone(h.last)
	var next []string
	for _, v := range s.Learning {
		if v != serial {
			next = append(next, v)
		}
	}
	s.Learning = next
	h.publishLocked(s, true, true)
}

func Parse(block string) []Transport {
	var out []Transport
	for _, line := range strings.Split(block, "\n") {
		f := strings.Fields(line)
		if len(f) < 2 || f[0] == "List" {
			continue
		}
		t := Transport{Serial: f[0], State: f[1], Kind: "other"}
		for _, v := range f[2:] {
			if strings.HasPrefix(v, "model:") {
				t.Model = strings.TrimPrefix(v, "model:")
			}
			if strings.HasPrefix(v, "transport_id:") {
				t.ID = strings.TrimPrefix(v, "transport_id:")
			}
			if strings.HasPrefix(v, "usb:") {
				t.Kind = "usb"
			}
		}
		if t.Kind != "usb" {
			switch {
			case strings.HasPrefix(t.Serial, "emulator-"):
			case strings.Contains(t.Serial, "_adb-tls-connect"), strings.Contains(t.Serial, "._adb._tcp"):
				t.Kind = "wifi"
			case strings.Contains(t.Serial, "_adb-tls"):
			case strings.Contains(t.Serial, ":"):
				if _, _, err := net.SplitHostPort(t.Serial); err == nil {
					t.Kind = "wifi"
				}
			default:
				t.Kind = "usb" // ADB short snapshots expose the USB serial only.
			}
		}
		out = append(out, t)
	}
	return out
}

// ReadBlock reads the CLI stream, whose Windows CRT expands payload LF to CRLF.
// Smart-socket callers must instead read exactly the advertised byte length.
func ReadBlock(r *bufio.Reader) (string, error) {
	var p [4]byte
	if _, err := io.ReadFull(r, p[:]); err != nil {
		return "", err
	}
	for _, c := range p {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return "", fmt.Errorf("invalid track prefix %q", p)
		}
	}
	n, _ := strconv.ParseUint(string(p[:]), 16, 16)
	var b strings.Builder
	b.Grow(int(n))
	for b.Len() < int(n) {
		v, err := r.ReadByte()
		if err != nil {
			return "", err
		}
		if v != '\r' {
			b.WriteByte(v)
		}
	}
	return b.String(), nil
}

// Track reconnects only after an actual stream failure. Healthy silence has no timer.
func Track(ctx context.Context, adbPath string, h *Hub) error {
	wake, stopWake, _ := resumeSignals()
	defer stopWake()
	var streamMu sync.Mutex
	var streamCancel context.CancelFunc
	var resumed atomic.Bool
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-wake:
				streamMu.Lock()
				if streamCancel != nil {
					resumed.Store(true)
					streamCancel()
				}
				streamMu.Unlock()
			}
		}
	}()
	var epoch uint64
	backoff := 250 * time.Millisecond
	long := true
	for ctx.Err() == nil {
		epoch++
		start := time.Now()
		args := []string{"track-devices"}
		if long {
			args = append(args, "-l")
		}
		streamCtx, cancel := context.WithCancel(ctx)
		streamMu.Lock()
		streamCancel = cancel
		streamMu.Unlock()
		got, err := trackOnce(streamCtx, adbPath, args, epoch, h)
		streamMu.Lock()
		streamCancel = nil
		cancel()
		streamMu.Unlock()
		if ctx.Err() != nil {
			break
		}
		h.Publish(Snapshot{Epoch: epoch, Error: fmt.Sprint(err)})
		if resumed.Swap(false) {
			backoff = 250 * time.Millisecond
			continue
		}
		if long && !got && strings.Contains(fmt.Sprint(err), "unknown host service") {
			long = false
			continue
		}
		if got && time.Since(start) > 5*time.Second {
			backoff = 250 * time.Millisecond
		}
		t := time.NewTimer(backoff)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		}
		if backoff < 8*time.Second {
			backoff *= 2
		}
	}
	return ctx.Err()
}

func trackOnce(ctx context.Context, path string, args []string, epoch uint64, h *Hub) (bool, error) {
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	c := exec.CommandContext(cctx, path, args...)
	hide(c)
	r, err := c.StdoutPipe()
	if err != nil {
		return false, err
	}
	var diagnostic limitedBuffer
	c.Stderr = &diagnostic
	if err = c.Start(); err != nil {
		return false, err
	}
	got := false
	seq := uint64(0)
	br := bufio.NewReader(r)
	for {
		block, e := ReadBlock(br)
		if e != nil {
			err = e
			break
		}
		got = true
		seq++
		h.Publish(Snapshot{Epoch: epoch, Sequence: seq, Available: true, Transports: Parse(block)})
	}
	// A bad prefix is not necessarily EOF: terminate the live client before Wait.
	cancel()
	_ = r.Close()
	_ = c.Wait()
	if diagnostic.text != "" {
		err = fmt.Errorf("%w: %s", err, diagnostic.text)
	}
	return got, err
}

type limitedBuffer struct{ text string }

func (b *limitedBuffer) Write(p []byte) (int, error) {
	if len(b.text) < 4096 {
		n := 4096 - len(b.text)
		if n > len(p) {
			n = len(p)
		}
		b.text += string(p[:n])
	}
	return len(p), nil
}
