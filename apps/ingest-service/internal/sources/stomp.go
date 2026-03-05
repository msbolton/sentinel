package sources

import (
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/go-stomp/stomp/v3"
	"go.uber.org/zap"

	"github.com/sentinel/ingest-service/internal/metrics"
	"github.com/sentinel/ingest-service/internal/models"
)

// STOMPListener connects to an ActiveMQ/STOMP broker, subscribes to a
// configured queue, and forwards received messages to the ingestion pipeline.
type STOMPListener struct {
	addr     string
	queue    string
	input    chan<- *models.IngestMessage
	logger   *zap.Logger
	metrics  *metrics.Metrics
	conn     *stomp.Conn
	sub      *stomp.Subscription
	stopOnce sync.Once
	stop     chan struct{}
	done     chan struct{}
}

// NewSTOMPListener creates a new STOMP listener for the given broker address and queue.
func NewSTOMPListener(addr, queue string, input chan<- *models.IngestMessage, logger *zap.Logger, m *metrics.Metrics) *STOMPListener {
	return &STOMPListener{
		addr:    addr,
		queue:   queue,
		input:   input,
		logger:  logger,
		metrics: m,
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
	}
}

// Start connects to the STOMP broker and begins consuming messages.
// It launches a background goroutine that reads messages until Stop is called.
func (l *STOMPListener) Start() error {
	if err := l.connect(); err != nil {
		return err
	}

	go l.consumeLoop()

	return nil
}

// connect establishes a TCP connection to the STOMP broker and subscribes
// to the configured queue.
func (l *STOMPListener) connect() error {
	netConn, err := net.DialTimeout("tcp", l.addr, 10*time.Second)
	if err != nil {
		return fmt.Errorf("stomp tcp dial to %s: %w", l.addr, err)
	}

	conn, err := stomp.Connect(netConn,
		stomp.ConnOpt.HeartBeat(10*time.Second, 10*time.Second),
	)
	if err != nil {
		netConn.Close()
		return fmt.Errorf("stomp connect to %s: %w", l.addr, err)
	}

	sub, err := conn.Subscribe(l.queue, stomp.AckClientIndividual)
	if err != nil {
		conn.Disconnect()
		return fmt.Errorf("stomp subscribe to %s: %w", l.queue, err)
	}

	l.conn = conn
	l.sub = sub

	l.logger.Info("stomp listener connected",
		zap.String("addr", l.addr),
		zap.String("queue", l.queue),
	)

	l.metrics.ActiveConnections.WithLabelValues(models.SourceSTOMP).Inc()

	return nil
}

// consumeLoop reads messages from the STOMP subscription and forwards them
// to the pipeline. It handles reconnection on failures.
func (l *STOMPListener) consumeLoop() {
	defer close(l.done)

	for {
		select {
		case <-l.stop:
			return
		default:
		}

		if l.sub == nil {
			l.reconnect()
			continue
		}

		select {
		case msg, ok := <-l.sub.C:
			if !ok {
				l.logger.Warn("stomp subscription channel closed, reconnecting")
				l.metrics.ActiveConnections.WithLabelValues(models.SourceSTOMP).Dec()
				l.sub = nil
				l.reconnect()
				continue
			}

			if msg.Err != nil {
				l.logger.Error("stomp message error", zap.Error(msg.Err))
				continue
			}

			ingestMsg := &models.IngestMessage{
				SourceType: models.SourceSTOMP,
				SourceAddr: l.queue,
				Payload:    msg.Body,
				ReceivedAt: time.Now().UTC(),
			}

			select {
			case l.input <- ingestMsg:
				// Acknowledge successful receipt.
				if err := l.conn.Ack(msg); err != nil {
					l.logger.Error("stomp ack failed", zap.Error(err))
				}
			default:
				l.logger.Warn("pipeline input full, nacking stomp message")
				if err := l.conn.Nack(msg); err != nil {
					l.logger.Error("stomp nack failed", zap.Error(err))
				}
				l.metrics.MessagesFailed.WithLabelValues(models.SourceSTOMP, "queue_full").Inc()
			}

		case <-l.stop:
			return
		}
	}
}

// reconnect attempts to re-establish the STOMP connection with exponential backoff.
func (l *STOMPListener) reconnect() {
	backoff := 1 * time.Second
	maxBackoff := 30 * time.Second

	for {
		select {
		case <-l.stop:
			return
		default:
		}

		l.logger.Info("attempting stomp reconnection",
			zap.String("addr", l.addr),
			zap.Duration("backoff", backoff),
		)

		select {
		case <-l.stop:
			return
		case <-time.After(backoff):
		}

		if err := l.connect(); err != nil {
			l.logger.Error("stomp reconnection failed", zap.Error(err))
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
			continue
		}

		l.logger.Info("stomp reconnected successfully")
		return
	}
}

// Stop disconnects from the STOMP broker and stops the consumer goroutine.
func (l *STOMPListener) Stop() {
	l.stopOnce.Do(func() {
		l.logger.Info("stopping stomp listener")
		close(l.stop)

		if l.sub != nil {
			if err := l.sub.Unsubscribe(); err != nil {
				l.logger.Error("stomp unsubscribe failed", zap.Error(err))
			}
		}

		if l.conn != nil {
			if err := l.conn.Disconnect(); err != nil {
				l.logger.Error("stomp disconnect failed", zap.Error(err))
			}
			l.metrics.ActiveConnections.WithLabelValues(models.SourceSTOMP).Dec()

			// Only wait for consumeLoop if it was started (conn != nil
			// means Start() succeeded and launched the goroutine).
			<-l.done
		}

		l.logger.Info("stomp listener stopped")
	})
}
