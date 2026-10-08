// Package notifications owns the optional device notification service, independently of casting.
package notifications

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"time"
)

const ProtocolVersion = 2

// A bounded 512KiB source PNG needs about 683KiB when base64 encoded. Message
// text retains its separate existing limits; only artwork needs this headroom.
const MaxFrame = 768 * 1024

var ErrProtocol = errors.New("notification protocol invalid")
var ErrOverflow = errors.New("notification queue full")

type Record struct {
	Key              string `json:"key"`
	Package          string `json:"package"`
	DisplayPackage   string `json:"displayPackage,omitempty"` // Display attribution only; Package retains the actual owner.
	App              string `json:"app"`
	User             int    `json:"user"`
	PostTime         int64  `json:"postTime"`
	MessageTime      int64  `json:"messageTime"`
	OnlyAlertOnce    bool   `json:"onlyAlertOnce"`
	Title            string `json:"title"`
	Body             string `json:"body"`
	VerificationCode string `json:"verificationCode,omitempty"` // Optional Xiaomi SMS metadata; never written to logs.
	IconID           string `json:"iconId,omitempty"`
}

type AppIcon struct {
	ID  string `json:"id"`
	PNG string `json:"png"`
}

type Frame struct {
	Version  int     `json:"v"`
	Session  string  `json:"session"`
	Sequence uint64  `json:"seq"`
	Type     string  `json:"type"`
	PID      int     `json:"pid,omitempty"`
	Cutoff   int64   `json:"cutoff,omitempty"`
	Record   Record  `json:"record,omitempty"`
	Icon     AppIcon `json:"icon,omitempty"`
	Code     string  `json:"code,omitempty"`
}

func ParseFrame(data []byte, session string) (Frame, error) {
	var frame Frame
	if len(data) > MaxFrame || json.Unmarshal(data, &frame) != nil || frame.Version != ProtocolVersion || frame.Session != session || frame.Sequence == 0 {
		return Frame{}, ErrProtocol
	}
	switch frame.Type {
	case "hello":
		if frame.PID <= 0 || frame.Cutoff <= 0 {
			return Frame{}, ErrProtocol
		}
	case "snapshot", "post":
		if frame.Record.Key == "" || len(frame.Record.Key) > 4096 || len(frame.Record.Package) > 512 || len(frame.Record.DisplayPackage) > 512 || len(frame.Record.App) > 1024 || len(frame.Record.Title) > 4096 || len(frame.Record.Body) > 32*1024 || len(frame.Record.IconID) > 64 || frame.Record.User != 0 || frame.Record.PostTime <= 0 {
			return Frame{}, ErrProtocol
		}
		// Invalid optional OEM metadata does not interrupt ordinary notifications.
		if !validVerificationCode(frame.Record) {
			frame.Record.VerificationCode = ""
		}
	case "remove":
		if frame.Record.Key == "" || len(frame.Record.Key) > 4096 {
			return Frame{}, ErrProtocol
		}
	case "ready":
	case "error":
		switch frame.Code {
		case "identity", "busy", "user", "permission", "unsupported", "startup":
		default:
			return Frame{}, ErrProtocol
		}
	case "icon":
		// Artwork is optional; malformed images must not suppress text notifications.
		if !validIcon(frame.Icon) {
			frame.Icon = AppIcon{}
		}
	default:
		return Frame{}, ErrProtocol
	}
	return frame, nil
}

func ShortID(text string) string {
	hash := sha256.Sum256([]byte(text))
	return hex.EncodeToString(hash[:8])
}

type Card struct {
	Package      string
	Group        string
	Tag          string
	Device       string
	Connection   string
	App          string
	Title        string
	Body         string
	Silent       bool
	Token        string
	Icon         AppIcon
	IconURI      string
	CopyCode     string
	CopyFallback time.Duration
	CopyIssuedAt time.Time
}

// Sink calls happen on the manager's serialized worker, never the casting/UI thread.
type Sink interface {
	Show(Card) error
	Remove(group, tag string) error
	Clear(group string) error
	Close() error
}
