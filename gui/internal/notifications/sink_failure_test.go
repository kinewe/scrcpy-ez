package notifications

import (
	"errors"
	"strings"
	"testing"
)

func TestSinkStatusDistinguishesFailureStages(t *testing.T) {
	for _, stage := range []string{"registration", "sender", "send", "remove", "clear", "disabled"} {
		status := sinkStatus(errors.Join(errors.New("private unrelated text"), &sinkFailure{stage: stage, code: 0x80070005}))
		if !strings.Contains(status, "0x80070005") || strings.Contains(status, "private") || strings.Contains(status, "请检查 Windows") {
			t.Fatal("failure stage/code lost or unrelated data leaked")
		}
	}
	if strings.Contains(sinkStatus(&sinkFailure{stage: "remove", code: 0x80070005}), "发送") {
		t.Fatal("withdrawal failure was mislabeled as sending")
	}
}
