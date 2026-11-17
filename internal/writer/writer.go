package writer

import (
	"context"
	"log/slog"
	"sync/atomic"
	"time"

	"github.com/trawlhq/trawl/internal/store"
)

const (
	maxBatch    = 500
	flushPeriod = 150 * time.Millisecond
)

// Writer serialises all span writes through one goroutine: SQLite allows a
// single writer, so batching here is correctness, not just performance.
type Writer struct {
	st      *store.Store
	ch      chan []store.Span
	written atomic.Int64
	dropped atomic.Int64
}

func New(st *store.Store, buffer int) *Writer {
	if buffer <= 0 {
		buffer = 2048
	}
	return &Writer{st: st, ch: make(chan []store.Span, buffer)}
}

func (w *Writer) Written() int64 { return w.written.Load() }
func (w *Writer) Dropped() int64 { return w.dropped.Load() }

// Submit is non-blocking. A full buffer is a backpressure signal the HTTP
// layer turns into 429, never into unbounded memory growth.
func (w *Writer) Submit(rows []store.Span) bool {
	if len(rows) == 0 {
		return true
	}
	select {
	case w.ch <- rows:
		return true
	default:
		w.dropped.Add(int64(len(rows)))
		return false
	}
}

func (w *Writer) Start(ctx context.Context) {
	go w.loop(ctx)
}

func (w *Writer) loop(ctx context.Context) {
	ticker := time.NewTicker(flushPeriod)
	defer ticker.Stop()

	pending := make([]store.Span, 0, maxBatch)
	flush := func() {
		if len(pending) == 0 {
			return
		}
		batch := pending
		pending = make([]store.Span, 0, maxBatch)
		if err := w.st.UpsertSpans(context.WithoutCancel(ctx), batch); err != nil {
			slog.Error("flush failed", "rows", len(batch), "error", err)
			w.dropped.Add(int64(len(batch)))
			return
		}
		w.written.Add(int64(len(batch)))
	}

	for {
		select {
		case <-ctx.Done():
			for {
				select {
				case rows := <-w.ch:
					pending = append(pending, rows...)
					if len(pending) >= maxBatch {
						flush()
					}
				default:
					flush()
					return
				}
			}
		case rows := <-w.ch:
			pending = append(pending, rows...)
			if len(pending) >= maxBatch {
				flush()
			}
		case <-ticker.C:
			flush()
		}
	}
}
