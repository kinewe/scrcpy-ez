package notifications

import "slices"

const (
	ModeOff       = "off"
	ModeOTP       = "otp"
	ModeAll       = "all"
	ModeWhitelist = "whitelist"
)

// Policy is keyed by physical identity, independent of ADB transport and casting.
// Packages remembers the last custom selection when a bulk mode is applied.
type Policy struct {
	Mode     string   `json:"mode"`
	Packages []string `json:"packages,omitempty"`
	Preview  *bool    `json:"preview,omitempty"`
	Other    bool     `json:"other,omitempty"`
	// Missing values enable ordinary notification detail actions by default.
	OpenEnabled *bool `json:"openEnabled,omitempty"`
	// Catalog is supplied from the current device app list, never persisted in settings.
	Catalog []string `json:"-"`
}

func (p Policy) Clone() Policy {
	p.Packages = slices.Clone(p.Packages)
	p.Catalog = slices.Clone(p.Catalog)
	if p.Preview != nil {
		value := *p.Preview
		p.Preview = &value
	}
	if p.OpenEnabled != nil {
		value := *p.OpenEnabled
		p.OpenEnabled = &value
	}
	return p
}

func (p Policy) DetailEnabled() bool {
	return p.OpenEnabled == nil || *p.OpenEnabled
}

func (p Policy) Enabled() bool {
	return p.Mode == ModeAll || p.Mode == ModeOTP || p.Mode == ModeWhitelist && (len(p.Packages) > 0 || p.Other)
}

func (p Policy) AllowsOpen(owner, display string) bool {
	if !p.DetailEnabled() || p.Mode == ModeOTP || p.Mode == ModeOff {
		return false
	}
	return p.allows(Record{Package: owner, DisplayPackage: display})
}

func (p Policy) allows(r Record) bool {
	switch p.Mode {
	case "", ModeAll: // Existing callers of NewState retain their original behavior.
		return true
	case ModeOTP:
		return recordOTP(r) != ""
	case ModeWhitelist:
		pkg := r.Package
		// The Android server only attributes verified system delegates to installed apps.
		// Match the displayed source, rather than allowing every delegated app together.
		if r.DisplayPackage != "" {
			pkg = r.DisplayPackage
		}
		if p.Catalog != nil && !slices.Contains(p.Catalog, pkg) {
			return p.Other
		}
		return slices.Contains(p.Packages, pkg) || p.Other && p.Catalog == nil
	default:
		return false
	}
}

func (o Options) policy(identity string) (Policy, bool) {
	p, exists := o.Policies[identity]
	if !exists {
		p.Mode = ModeAll
	}
	preview := o.Preview
	if p.Preview != nil {
		preview = *p.Preview
	}
	return p, preview
}
