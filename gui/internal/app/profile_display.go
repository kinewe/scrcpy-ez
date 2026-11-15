package app

import "time"

func (s *ProfileStore) displayVersion() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.version
}

type addressObservation struct {
	entry   *DeviceEntry
	version uint64
}

func (s *ProfileStore) noteAddressEvidenceLocked(e *DeviceEntry, addr string) {
	for i := range e.Addrs {
		if e.Addrs[i].Addr == addr {
			s.addressEvidence++
			e.Addrs[i].evidence = s.addressEvidence
			return
		}
	}
}

func (s *ProfileStore) observeAddress(key, addr string) addressObservation {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.resolveLocked(key)
	if ok {
		for _, a := range e.Addrs {
			if a.Addr == addr && a.State == AddrStateActive {
				return addressObservation{entry: e, version: a.evidence}
			}
		}
	}
	return addressObservation{}
}

// A probe from before a newer USB/transport/mDNS success cannot invalidate it.
func (s *ProfileStore) failObservedAddress(key, addr, mode string, observation addressObservation) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.resolveLocked(key)
	if !ok || e != observation.entry {
		return false
	}
	for _, a := range e.Addrs {
		if a.Addr == addr && a.State == AddrStateActive && a.evidence == observation.version {
			s.addrFailModeLocked(e, addr, mode, time.Now())
			_ = s.persistLocked()
			return true
		}
	}
	return false
}

// A successful absence probe can only refresh the exact active address it
// observed. It must not recreate a retired port or reverse a newer stale fact.
func (s *ProfileStore) succeedObservedAddress(key, addr, mode string, observation addressObservation) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.resolveLocked(key)
	if !ok || e != observation.entry || !s.snapshotAddressAllowedLocked(e, addr) {
		return false
	}
	for _, a := range e.Addrs {
		if a.Addr == addr && a.State == AddrStateActive && a.evidence == observation.version {
			changed := s.addrSuccessModeLocked(e, addr, mode, time.Now())
			changed = s.applyWirelessFormLocked(e, mode) || changed
			if changed {
				_ = s.persistLocked()
			}
			return true
		}
	}
	return false
}

// The card cache is a projection, not an independent availability source. A
// missed mutation notification is repaired before serving the next snapshot.
// This path only updates the projection; it does not launch recovery/learning.
func (a *App) ensureProfileDisplayCurrent() {
	a.displayMu.Lock()
	defer a.displayMu.Unlock()
	if a.displayInitialized && (a.displayProfileVersion != a.profiles.displayVersion() || a.displayPhysVersion != a.physicalDisplayVersion()) {
		a.projectDisplayLocked(a.displayBase, "档案或物理规格变化")
	}
}
