package server

import (
	"context"

	"github.com/go-kit/log"
	"github.com/go-kit/log/level"
	"github.com/rfratto/viceroy/internal/fine"
)

// NewLoggingMiddleware returns a new logging middleware.
func NewLoggingMiddleware(l log.Logger) Middleware {
	if l == nil {
		l = log.NewNopLogger()
	}
	return &loggingMiddlware{l: l}
}

type loggingMiddlware struct {
	l log.Logger
}

func (lm *loggingMiddlware) HandleRequest(ctx context.Context, hdr *fine.RequestHeader, req fine.Request, invoker Invoker) (fine.Response, error) {
	level.Debug(lm.l).Log("msg", "starting request", "op", hdr.Op, "id", hdr.RequestID)
	resp, err := invoker(ctx, hdr, req)
	level.Debug(lm.l).Log("msg", "finished request", "op", hdr.Op, "id", hdr.RequestID, "err", err)
	return resp, err
}
