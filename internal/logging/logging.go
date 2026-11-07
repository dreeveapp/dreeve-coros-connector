package logging

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"slices"
	"strings"
	"sync"
)

type Format string

const (
	Text Format = "text"
	JSON Format = "json"
)

const redacted = "[REDACTED]"

func ParseFormat(raw string) (Format, error) {
	switch f := Format(strings.ToLower(raw)); f {
	case "":
		return Text, nil
	case Text, JSON:
		return f, nil
	}
	return "", fmt.Errorf("LOG_FORMAT must be text or json, got %q", raw)
}

func ParseLevel(raw string) (slog.Level, error) {
	switch strings.ToLower(raw) {
	case "", "info":
		return slog.LevelInfo, nil
	case "debug":
		return slog.LevelDebug, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	}
	return 0, fmt.Errorf("LOG_LEVEL must be debug, info, warn or error, got %q", raw)
}

type Secrets struct {
	mu     sync.RWMutex
	values []string
}

func (s *Secrets) Add(value string) {
	if value == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !slices.Contains(s.values, value) {
		s.values = append(s.values, value)
	}
}

func (s *Secrets) redact(text string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, v := range s.values {
		text = strings.ReplaceAll(text, v, redacted)
	}
	return text
}

func New(w io.Writer, level slog.Level, format Format, secrets *Secrets) *slog.Logger {
	opts := &slog.HandlerOptions{Level: level}
	var next slog.Handler = slog.NewTextHandler(w, opts)
	if format == JSON {
		next = slog.NewJSONHandler(w, opts)
	}
	return slog.New(&redactor{next: next, secrets: secrets})
}

type redactor struct {
	next    slog.Handler
	secrets *Secrets
}

func (h *redactor) Enabled(ctx context.Context, level slog.Level) bool {
	return h.next.Enabled(ctx, level)
}

func (h *redactor) Handle(ctx context.Context, r slog.Record) error {
	clean := slog.NewRecord(r.Time, r.Level, h.secrets.redact(r.Message), r.PC)
	r.Attrs(func(a slog.Attr) bool {
		clean.AddAttrs(h.attr(a))
		return true
	})
	return h.next.Handle(ctx, clean)
}

func (h *redactor) WithAttrs(attrs []slog.Attr) slog.Handler {
	clean := make([]slog.Attr, len(attrs))
	for i, a := range attrs {
		clean[i] = h.attr(a)
	}
	return &redactor{next: h.next.WithAttrs(clean), secrets: h.secrets}
}

func (h *redactor) WithGroup(name string) slog.Handler {
	return &redactor{next: h.next.WithGroup(name), secrets: h.secrets}
}

func secretKey(key string) bool {
	switch strings.ToLower(key) {
	case "token", "accesstoken", "password", "pwd":
		return true
	}
	return false
}

func (h *redactor) attr(a slog.Attr) slog.Attr {
	a.Value = a.Value.Resolve()
	if secretKey(a.Key) {
		return slog.String(a.Key, redacted)
	}
	switch a.Value.Kind() {
	case slog.KindString:
		return slog.String(a.Key, h.secrets.redact(a.Value.String()))
	case slog.KindGroup:
		group := a.Value.Group()
		clean := make([]slog.Attr, len(group))
		for i, g := range group {
			clean[i] = h.attr(g)
		}
		return slog.Attr{Key: a.Key, Value: slog.GroupValue(clean...)}
	case slog.KindAny:
		switch v := a.Value.Any().(type) {
		case error:
			return slog.String(a.Key, h.secrets.redact(v.Error()))
		case fmt.Stringer:
			return slog.String(a.Key, h.secrets.redact(v.String()))
		default:
			if text := fmt.Sprint(v); h.secrets.redact(text) != text {
				return slog.String(a.Key, h.secrets.redact(text))
			}
		}
	}
	return a
}
