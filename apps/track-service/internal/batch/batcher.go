package batch

import (
	"context"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/sentinel/track-service/internal/models"
	"github.com/sentinel/track-service/internal/store"
)

const (
	batchSize      = 100
	flushInterval  = 1 * time.Second
	maxBufferSize  = 10_000
)

// Batcher buffers track points and flushes them in bulk to the database.
// Flushes when buffer reaches batchSize or flushInterval elapses.
type Batcher struct {
	store  *store.TrackStore
	logger *zap.Logger

	mu         sync.Mutex
	buffer     []models.BufferedPoint
	isFlushing bool

	stopCh chan struct{}
	wg     sync.WaitGroup
}

// NewBatcher creates a new track point batcher.
func NewBatcher(store *store.TrackStore, logger *zap.Logger) *Batcher {
	b := &Batcher{
		store:  store,
		logger: logger,
		buffer: make([]models.BufferedPoint, 0, batchSize),
		stopCh: make(chan struct{}),
	}

	b.wg.Add(1)
	go b.flushLoop()

	return b
}

// AddPoint adds a track point to the buffer. Triggers flush if buffer is full.
func (b *Batcher) AddPoint(point models.BufferedPoint) {
	b.mu.Lock()
	defer b.mu.Unlock()

	if len(b.buffer) >= maxBufferSize {
		b.logger.Warn("buffer full, dropping incoming point",
			zap.Int("maxSize", maxBufferSize),
		)
		return
	}

	b.buffer = append(b.buffer, point)

	if len(b.buffer) >= batchSize {
		go b.flush()
	}
}

// Stop stops the flush loop and performs a final flush.
func (b *Batcher) Stop() {
	close(b.stopCh)
	b.wg.Wait()
	b.flush()
}

func (b *Batcher) flushLoop() {
	defer b.wg.Done()

	ticker := time.NewTicker(flushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			b.flush()
		case <-b.stopCh:
			return
		}
	}
}

func (b *Batcher) flush() {
	b.mu.Lock()
	if b.isFlushing || len(b.buffer) == 0 {
		b.mu.Unlock()
		return
	}
	b.isFlushing = true
	points := make([]models.BufferedPoint, len(b.buffer))
	copy(points, b.buffer)
	b.buffer = b.buffer[:0]
	b.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := b.store.BulkInsert(ctx, points); err != nil {
		b.logger.Error("failed to flush track points",
			zap.Int("count", len(points)),
			zap.Error(err),
		)
		// Re-add failed points for retry
		b.mu.Lock()
		combined := len(b.buffer) + len(points)
		if combined > maxBufferSize {
			dropped := combined - maxBufferSize
			b.logger.Error("dropping points on retry to stay within buffer limit",
				zap.Int("dropped", dropped),
			)
			keepCount := maxBufferSize - len(b.buffer)
			b.buffer = append(points[len(points)-keepCount:], b.buffer...)
		} else {
			b.buffer = append(points, b.buffer...)
		}
		b.mu.Unlock()
	} else {
		b.logger.Debug("flushed track points", zap.Int("count", len(points)))
	}

	b.mu.Lock()
	b.isFlushing = false
	b.mu.Unlock()
}

// BufferSize returns the current buffer size for monitoring.
func (b *Batcher) BufferSize() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.buffer)
}
