package util

import (
	"context"
	"log/slog"
	"runtime"
)

func NonblockingChSend[T any](ch chan T, msg T) {
	_, file, line, _ := runtime.Caller(1)
	select {
	case ch <- msg:
	default:
		slog.Warn("channel was blocked, message discarded", "msg", msg, "file", file, "line", line)
	}
}

func NonblockingChSendCtx[T any](ctx context.Context, ch chan T, msg T) {
	clientId := ctx.Value("clientID")
	_, file, line, _ := runtime.Caller(1)
	select {
	case ch <- msg:
		//slog.DebugContext(ctx, "sending msg to ch", "msg", msg, "clientId", clientId)
	default:
		slog.WarnContext(ctx, "channel was blocked, message discarded", "msg", msg, "clientId", clientId, "file", file, "line", line)
	}
}

type Msg struct {
	Key   string
	Value interface{}
}
