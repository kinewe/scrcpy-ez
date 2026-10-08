package rootrepair

import (
	"context"
	"sync/atomic"
	"testing"
)

func TestCanceledRouteDoesNotSuppressFreshAttempt(t *testing.T) {
	var calls atomic.Int32
	c := NewCoordinator("", func(context.Context, Request) (Report, error) {
		if calls.Add(1) == 1 {
			return Report{}, context.Canceled
		}
		return Report{Status: "repaired"}, nil
	})
	if e := c.SetEnabled("PHONE_A", true); e != nil {
		t.Fatal(e)
	}
	r := Request{Serial: "USB_A", Identity: "PHONE_A", Key: "1/USB_A/1"}
	if o := c.Repair(context.Background(), r); o.Status != "canceled" {
		t.Fatal(o)
	}
	r.Key = "1/USB_A/2"
	if o := c.Repair(context.Background(), r); !o.Accepted() || calls.Load() != 2 {
		t.Fatal(o, calls.Load())
	}
}
