package util

import "log/slog"

func NonblockingChSend[T any](ch chan T, msg T) {
	select {
	case ch <- msg:
	default:
		slog.Warn("channel was blocked, message discarded", "msg", msg)
	}
}
