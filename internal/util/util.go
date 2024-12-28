package util

import (
	"context"
	"log/slog"
)

func NonblockingChSend[T any](ch chan T, msg T) {
	select {
	case ch <- msg:
	default:
		slog.Warn("channel was blocked, message discarded", "msg", msg)
	}
}

func NonblockingChSendCtx[T any](ctx context.Context, ch chan T, msg T) {
	clientId := ctx.Value("clientID")

	select {
	case ch <- msg:
		//slog.DebugContext(ctx, "sending msg to ch", "msg", msg, "clientId", clientId)
	default:
		slog.WarnContext(ctx, "channel was blocked, message discarded", "msg", msg, "clientId", clientId)
	}
}
