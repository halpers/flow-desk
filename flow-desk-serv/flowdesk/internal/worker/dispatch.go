package worker

import (
	"context"
	"flowdesk/internal/logic"
	"flowdesk/internal/svc"
	"github.com/zeromicro/go-zero/core/logx"
	"time"
)

const pollInterval = 500 * time.Millisecond
const batchSize = 10

func Run(ctx context.Context, s *svc.ServiceContext) {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()
	ticks := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		for range batchSize {
			found, err := logic.DispatchNext(ctx, s)
			if err != nil {
				logx.Error("dispatch persistence failed")
				break
			}
			if !found {
				break
			}
		}
		ticks++
		if ticks%batchSize == 0 {
			if err := logic.ExpireOverdueRuns(ctx, s); err != nil {
				logx.Error("run expiry scan failed")
			}
		}
	}
}
