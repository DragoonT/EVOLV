package server

import (
	"context"
	"time"
)

// provName returns the active provider's name (thread-safe enough for status).
func (s *Server) provName() string {
	if s.ag == nil {
		return "unknown"
	}
	return s.ag.ProviderName()
}

func contextWithTimeout(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(parent, 30*time.Second)
}
