package sources

import (
	"fmt"
	"strings"
	"sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	"go.uber.org/zap"

	"github.com/sentinel/ingest-service/internal/metrics"
	"github.com/sentinel/ingest-service/internal/models"
)

// MQTTListener connects to an MQTT broker, subscribes to configured topics,
// and forwards received messages to the ingestion pipeline input channel.
type MQTTListener struct {
	client   mqtt.Client
	broker   string
	topics   []string
	input    chan<- *models.IngestMessage
	logger   *zap.Logger
	metrics  *metrics.Metrics
	stopOnce sync.Once
	done     chan struct{}
}

// NewMQTTListener creates a new MQTT listener. The topics parameter is a
// comma-separated list of MQTT topic patterns (wildcards supported).
func NewMQTTListener(broker, topics string, input chan<- *models.IngestMessage, logger *zap.Logger, m *metrics.Metrics) *MQTTListener {
	topicList := strings.Split(topics, ",")
	for i := range topicList {
		topicList[i] = strings.TrimSpace(topicList[i])
	}

	return &MQTTListener{
		broker:  broker,
		topics:  topicList,
		input:   input,
		logger:  logger,
		metrics: m,
		done:    make(chan struct{}),
	}
}

// Start connects to the MQTT broker and begins subscribing to topics.
// It blocks until the connection is established or an error occurs.
func (l *MQTTListener) Start() error {
	opts := mqtt.NewClientOptions().
		AddBroker(l.broker).
		SetClientID(fmt.Sprintf("sentinel-ingest-%d", time.Now().UnixNano())).
		SetAutoReconnect(true).
		SetMaxReconnectInterval(30 * time.Second).
		SetConnectionLostHandler(l.onConnectionLost).
		SetOnConnectHandler(l.onConnect).
		SetOrderMatters(false).
		SetCleanSession(true)

	l.client = mqtt.NewClient(opts)

	token := l.client.Connect()
	if !token.WaitTimeout(10 * time.Second) {
		return fmt.Errorf("mqtt connection timeout to %s", l.broker)
	}
	if token.Error() != nil {
		return fmt.Errorf("mqtt connection failed: %w", token.Error())
	}

	l.logger.Info("mqtt listener connected",
		zap.String("broker", l.broker),
		zap.Strings("topics", l.topics),
	)

	l.metrics.ActiveConnections.WithLabelValues(models.SourceMQTT).Inc()

	return nil
}

// onConnect is called when the MQTT client connects (or reconnects). It
// subscribes to all configured topics.
func (l *MQTTListener) onConnect(client mqtt.Client) {
	l.logger.Info("mqtt connected, subscribing to topics")

	filters := make(map[string]byte)
	for _, topic := range l.topics {
		filters[topic] = 1 // QoS 1 (at least once)
	}

	token := client.SubscribeMultiple(filters, l.onMessage)
	if !token.WaitTimeout(10 * time.Second) {
		l.logger.Error("mqtt subscription timeout")
		return
	}
	if token.Error() != nil {
		l.logger.Error("mqtt subscription failed", zap.Error(token.Error()))
		return
	}

	l.logger.Info("mqtt subscriptions active", zap.Strings("topics", l.topics))
}

// onConnectionLost is called when the MQTT connection drops unexpectedly.
func (l *MQTTListener) onConnectionLost(_ mqtt.Client, err error) {
	l.logger.Warn("mqtt connection lost, will reconnect", zap.Error(err))
	l.metrics.ActiveConnections.WithLabelValues(models.SourceMQTT).Dec()
}

// onMessage handles incoming MQTT messages and forwards them to the pipeline.
func (l *MQTTListener) onMessage(_ mqtt.Client, msg mqtt.Message) {
	ingestMsg := &models.IngestMessage{
		SourceType: models.SourceMQTT,
		SourceAddr: msg.Topic(),
		Payload:    msg.Payload(),
		ReceivedAt: time.Now().UTC(),
	}

	select {
	case l.input <- ingestMsg:
		// Message queued successfully.
	default:
		l.logger.Warn("pipeline input full, dropping mqtt message",
			zap.String("topic", msg.Topic()),
		)
		l.metrics.MessagesFailed.WithLabelValues(models.SourceMQTT, "queue_full").Inc()
	}
}

// Stop disconnects from the MQTT broker and releases resources.
func (l *MQTTListener) Stop() {
	l.stopOnce.Do(func() {
		l.logger.Info("stopping mqtt listener")
		if l.client != nil && l.client.IsConnected() {
			l.client.Disconnect(5000)
			l.metrics.ActiveConnections.WithLabelValues(models.SourceMQTT).Dec()
		}
		close(l.done)
		l.logger.Info("mqtt listener stopped")
	})
}
