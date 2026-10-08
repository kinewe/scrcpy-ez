//go:build !windows || !cgo

package notifications

func NewWindowsSink() (Sink, error)            { return nil, ErrUnavailable }
func RunIfPassiveRequested(args []string) bool { return false }
