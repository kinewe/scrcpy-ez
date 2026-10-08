package notifications

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Status struct {
	Identity string `json:"identity"`
	State    string `json:"state"`
	Text     string `json:"text"`
}

type Options struct {
	Preview      bool
	CopyFallback time.Duration
	Policies     map[string]Policy
}
type reconcile struct {
	targets []Target
	options Options
}
type delivery struct {
	identity   string
	generation uint64
	frame      Frame
	done       bool
	err        error
}
type worker struct {
	target     Target
	generation uint64
	cancel     context.CancelFunc
	state      *State
	name       string
	connection string
	done       chan struct{}
	failed     bool
}

type Manager struct {
	source        Source
	artwork       func(string, string) Artwork
	factory       func() (Sink, error)
	reconcile     chan reconcile
	delivery      chan delivery
	done          chan struct{}
	cancel        context.CancelFunc
	mu            sync.Mutex
	requestMu     sync.Mutex
	status        map[string]Status
	retryCooldown time.Duration
}

func NewManager(parent context.Context, source Source, factory func() (Sink, error), artwork ...func(string, string) Artwork) *Manager {
	ctx, cancel := context.WithCancel(parent)
	m := &Manager{source: source, factory: factory, reconcile: make(chan reconcile, 1), delivery: make(chan delivery, 128), done: make(chan struct{}), cancel: cancel, status: make(map[string]Status), retryCooldown: 30 * time.Second}
	if len(artwork) > 0 {
		m.artwork = artwork[0]
	}
	go m.run(ctx)
	return m
}

// Latest desired device state replaces old requests; this call never waits for ADB/Windows.
func (m *Manager) Reconcile(targets []Target, options Options) {
	m.requestMu.Lock()
	defer m.requestMu.Unlock()
	policies := make(map[string]Policy, len(options.Policies))
	for id, policy := range options.Policies {
		policies[id] = policy.Clone()
	}
	options.Policies = policies
	r := reconcile{targets: append([]Target(nil), targets...), options: options}
	select {
	case <-m.done:
		return
	default:
	}
	select {
	case m.reconcile <- r:
		return
	default:
	}
	select {
	case <-m.reconcile:
	default:
	}
	select {
	case m.reconcile <- r:
	case <-m.done:
	default:
	}
}

func (m *Manager) Status() []Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Status, 0, len(m.status))
	for _, status := range m.status {
		out = append(out, status)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Identity < out[j].Identity })
	return out
}

func (m *Manager) setStatus(id, state, text string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.status[id] = Status{Identity: id, State: state, Text: text}
}

func (m *Manager) Close() { m.cancel(); <-m.done }

func (m *Manager) run(ctx context.Context) {
	defer close(m.done)
	workers := make(map[string]*worker)
	retiring := make(map[string]<-chan struct{})
	var sink Sink
	var options Options
	var generation uint64
	var sinkFailure string
	var jobs sync.WaitGroup
	stopWorker := func(w *worker) {
		w.cancel()
		retiring[w.target.Identity] = w.done
		if sink != nil {
			_ = w.state.Clear(sink)
		}
	}
	defer func() {
		for _, w := range workers {
			stopWorker(w)
		}
		jobs.Wait()
		if sink != nil {
			_ = sink.Close()
		}
	}()
	for {
		select {
		case <-ctx.Done():
			return
		case desired := <-m.reconcile:
			for id, done := range retiring {
				select {
				case <-done:
					delete(retiring, id)
				default:
				}
			}
			wanted := make(map[string]Target)
			for _, target := range desired.targets {
				if target.Identity != "" && target.Serial != "" {
					wanted[target.Identity] = target
				}
			}
			m.mu.Lock()
			for id := range m.status {
				if _, exists := wanted[id]; !exists {
					delete(m.status, id)
				}
			}
			m.mu.Unlock()
			for id, w := range workers {
				target, exists := wanted[id]
				if !exists || target.Serial != w.target.Serial || target.DeviceSerial != w.target.DeviceSerial || target.Epoch != w.target.Epoch || target.ServerEpoch != w.target.ServerEpoch {
					stopWorker(w)
					delete(workers, id)
					m.mu.Lock()
					delete(m.status, id)
					m.mu.Unlock()
					continue
				}
				w.name = target.Name
				w.state.device = target.Name
				w.state.connection = target.Connection
				w.state.copyFallback = desired.options.CopyFallback
				w.connection = target.Connection
				if m.artwork != nil {
					w.state.artwork = func(pkg string) Artwork { return m.artwork(id, pkg) }
				}
				if sink != nil {
					policy, preview := desired.options.policy(id)
					if err := w.state.SetPolicy(policy, preview, sink); err != nil {
						w.failed = true
						w.cancel()
						m.setStatus(id, "unavailable", sinkStatus(err))
					}
				}
			}
			options = desired.options
			if len(wanted) == 0 {
				sinkFailure = ""
				if sink != nil {
					_ = sink.Close()
					sink = nil
				}
				continue
			}
			if sink == nil {
				keys := make([]string, 0, len(wanted))
				for id, target := range wanted {
					keys = append(keys, id+"/"+target.Serial+"/"+strconv.FormatUint(target.Epoch, 10)+"/"+strconv.FormatUint(target.ServerEpoch, 10))
				}
				sort.Strings(keys)
				signature := ShortID(strings.Join(keys, "\n"))
				if signature == sinkFailure {
					continue
				}
				var err error
				sink, err = m.factory()
				if err != nil {
					sinkFailure = signature
					for id := range wanted {
						m.setStatus(id, "unavailable", sinkStatus(err))
					}
					continue
				}
				sinkFailure = ""
			}
			for id, target := range wanted {
				if _, exists := workers[id]; exists {
					continue
				}
				generation++
				workerCtx, cancel := context.WithCancel(ctx)
				policy, preview := options.policy(id)
				w := &worker{target: target, name: target.Name, generation: generation, cancel: cancel, state: NewState(id, target.Name, preview), done: make(chan struct{})}
				w.state.policy = policy.Clone()
				w.state.connection = target.Connection
				w.state.copyFallback = options.CopyFallback
				w.connection = target.Connection
				workers[id] = w
				if m.artwork != nil {
					w.state.artwork = func(pkg string) Artwork { return m.artwork(id, pkg) }
				}
				m.setStatus(id, "connecting", "正在连接通知服务")
				jobs.Add(1)
				previous := retiring[id]
				delete(retiring, id)
				go func(w *worker, previous <-chan struct{}) {
					defer jobs.Done()
					defer close(w.done)
					// Ownership on Android is released before a replacement listener starts.
					if previous != nil {
						select {
						case <-previous:
						case <-workerCtx.Done():
							<-previous // Preserve the cleanup chain across rapid off/on/off toggles.
							return
						}
					}
					// Transport loss must recover even when ADB stays online and the
					// device epoch is unchanged. Bound each burst to three attempts,
					// then cool down; capability/protocol failures still stop below.
					for attempt := 0; ; attempt++ {
						delay := time.Duration(attempt) * time.Second
						if attempt == 3 {
							delay = m.retryCooldown
							attempt = 0
						}
						if delay > 0 {
							timer := time.NewTimer(delay)
							select {
							case <-workerCtx.Done():
								timer.Stop()
								return
							case <-timer.C:
							}
						}
						result := m.source.Run(workerCtx, w.target, func(frame Frame) error {
							select {
							case <-workerCtx.Done():
								return workerCtx.Err()
							case m.delivery <- delivery{identity: w.target.Identity, generation: w.generation, frame: frame}:
								return nil
							default:
								return ErrOverflow
							}
						})
						if workerCtx.Err() != nil {
							return
						}
						select {
						case m.delivery <- delivery{identity: w.target.Identity, generation: w.generation, done: true, err: result}:
						case <-workerCtx.Done():
							return
						}
						if errors.Is(result, ErrUnavailable) || errors.Is(result, ErrProtocol) {
							return
						}
					}
				}(w, previous)
			}
		case event := <-m.delivery:
			w := workers[event.identity]
			if w == nil || w.generation != event.generation || w.failed {
				continue
			}
			if event.done {
				_ = w.state.Clear(sink)
				policy, preview := options.policy(w.target.Identity)
				w.state = NewState(w.target.Identity, w.name, preview)
				w.state.policy = policy.Clone()
				w.state.connection = w.connection
				w.state.copyFallback = options.CopyFallback
				if m.artwork != nil {
					w.state.artwork = func(pkg string) Artwork { return m.artwork(w.target.Identity, pkg) }
				}
				if errors.Is(event.err, ErrUnavailable) || errors.Is(event.err, ErrProtocol) {
					m.setStatus(event.identity, "unavailable", sourceStatus(event.err))
				} else {
					m.setStatus(event.identity, "connecting", sourceStatus(event.err))
				}
				continue
			}
			if err := w.state.Apply(event.frame, sink); err != nil {
				w.failed = true
				w.cancel()
				_ = w.state.Clear(sink)
				text := sinkStatus(err)
				if errors.Is(err, ErrProtocol) {
					text = sourceStatus(err)
				}
				m.setStatus(event.identity, "unavailable", text)
				continue
			}
			if event.frame.Type == "ready" {
				m.setStatus(event.identity, "active", "正在同步新通知")
			}
		}
	}
}
