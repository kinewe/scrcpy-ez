//go:build !windows

package rootrepair

import (
	"context"
	"os/exec"
)

func hide(*exec.Cmd)                                               {}
func PrepareLocked(ctx context.Context, o Options) (Report, error) { return Prepare(ctx, o) }
