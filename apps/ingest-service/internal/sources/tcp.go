package sources

import (
	"bufio"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"

	"github.com/sentinel/ingest-service/internal/metrics"
	"github.com/sentinel/ingest-service/internal/models"
)

// TCPListener accepts raw TCP connections and reads line-delimited messages.
// Each line is forwarded to the ingestion pipeline as a separate message.
// It supports multiple concurrent connections with graceful shutdown.
type TCPListener struct {
	addr     string
	feedID   string
	input    chan<- *models.IngestMessage
	logger   *zap.Logger
	metrics  *metrics.Metrics
	listener net.Listener
	conns    sync.Map
	connID   atomic.Int64
	stopOnce sync.Once
	stop     chan struct{}
	wg       sync.WaitGroup
}

// NewTCPListener creates a new TCP listener on the specified address.
func NewTCPListener(addr, feedID string, input chan<- *models.IngestMessage, logger *zap.Logger, m *metrics.Metrics) *TCPListener {
	return &TCPListener{
		addr:    addr,
		feedID:  feedID,
		input:   input,
		logger:  logger,
		metrics: m,
		stop:    make(chan struct{}),
	}
}

// Start binds the TCP listener and begins accepting connections.
func (l *TCPListener) Start() error {
	var err error
	l.listener, err = net.Listen("tcp", l.addr)
	if err != nil {
		return fmt.Errorf("tcp listen on %s: %w", l.addr, err)
	}

	l.logger.Info("tcp listener started", zap.String("addr", l.addr))

	l.wg.Add(1)
	go l.acceptLoop()

	return nil
}

// acceptLoop accepts new TCP connections and spawns a goroutine for each.
func (l *TCPListener) acceptLoop() {
	defer l.wg.Done()

	for {
		conn, err := l.listener.Accept()
		if err != nil {
			select {
			case <-l.stop:
				return
			default:
				l.logger.Error("tcp accept error", zap.Error(err))
				continue
			}
		}

		id := l.connID.Add(1)
		l.conns.Store(id, conn)
		l.metrics.ActiveConnections.WithLabelValues(models.SourceTCP).Inc()

		l.wg.Add(1)
		go l.handleConnection(id, conn)
	}
}

// handleConnection reads line-delimited messages from a single TCP connection
// and forwards them to the pipeline.
func (l *TCPListener) handleConnection(id int64, conn net.Conn) {
	defer l.wg.Done()
	defer func() {
		conn.Close()
		l.conns.Delete(id)
		l.metrics.ActiveConnections.WithLabelValues(models.SourceTCP).Dec()
		l.logger.Debug("tcp connection closed",
			zap.Int64("conn_id", id),
			zap.String("remote_addr", conn.RemoteAddr().String()),
		)
	}()

	remoteAddr := conn.RemoteAddr().String()
	l.logger.Debug("tcp connection accepted",
		zap.Int64("conn_id", id),
		zap.String("remote_addr", remoteAddr),
	)

	scanner := bufio.NewScanner(conn)
	// Allow up to 1MB per line for large sensor payloads.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for scanner.Scan() {
		select {
		case <-l.stop:
			return
		default:
		}

		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		// Copy the line since scanner reuses the buffer.
		payload := make([]byte, len(line))
		copy(payload, line)

		msg := &models.IngestMessage{
			SourceType: models.SourceTCP,
			SourceAddr: remoteAddr,
			Payload:    payload,
			ReceivedAt: time.Now().UTC(),
			FeedID:     l.feedID,
		}

		select {
		case l.input <- msg:
			// Message queued.
		default:
			l.logger.Warn("pipeline input full, dropping tcp message",
				zap.String("remote_addr", remoteAddr),
			)
			l.metrics.MessagesFailed.WithLabelValues(models.SourceTCP, "queue_full").Inc()
		}
	}

	if err := scanner.Err(); err != nil {
		select {
		case <-l.stop:
			// Expected during shutdown.
		default:
			l.logger.Error("tcp read error",
				zap.Int64("conn_id", id),
				zap.String("remote_addr", remoteAddr),
				zap.Error(err),
			)
		}
	}
}

// Stop closes the listener and all active connections, then waits for
// all handler goroutines to finish.
func (l *TCPListener) Stop() {
	l.stopOnce.Do(func() {
		l.logger.Info("stopping tcp listener")
		close(l.stop)

		if l.listener != nil {
			l.listener.Close()
		}

		// Close all active connections to unblock readers.
		l.conns.Range(func(key, value any) bool {
			if conn, ok := value.(net.Conn); ok {
				conn.Close()
			}
			return true
		})

		l.wg.Wait()
		l.logger.Info("tcp listener stopped")
	})
}
