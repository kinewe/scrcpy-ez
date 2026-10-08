package rootrepair

import (
	"context"
	"os/exec"
	"strings"
)

type boundedOutput struct{ b strings.Builder }

func (b *boundedOutput) Write(p []byte) (int, error) {
	n := len(p)
	if left := 65536 - b.b.Len(); left > 0 {
		if len(p) > left {
			p = p[:left]
		}
		_, _ = b.b.Write(p)
	}
	return n, nil
}

func CommandExecutor(adb string) Execute {
	return func(ctx context.Context, args []string, script string) (string, error) {
		c := exec.CommandContext(ctx, adb, args...)
		hide(c)
		if script != "" {
			c.Stdin = strings.NewReader(script)
		}
		var out boundedOutput
		c.Stdout, c.Stderr = &out, &out
		err := c.Run()
		return out.b.String(), err
	}
}
