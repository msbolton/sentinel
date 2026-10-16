// Package mqtt subscribes to topics on an MQTT broker; every message is a
// frame. Connection is retried until ctx ends.
package mqtt

import (
	"context"
	"errors"
	"fmt"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"

	"github.com/sentinel/ingest-service/internal/source/transport"
)

const (
	connectTimeout = 10 * time.Second
	maxBackoff     = 30 * time.Second
)

func init() {
	transport.Register(transport.Definition{
		Kind: "mqtt", Type: transport.KindMessage,
		Description: "Subscribe to MQTT topics; every message is one frame.",
		New:         New,
	})
}

// Subscriber is the mqtt transport.
type Subscriber struct {
	cfg transport.Config
	transport.StateTracker
}

// New validates the config and builds the transport.
func New(cfg transport.Config) (transport.Transport, error) {
	if cfg.BrokerURL == "" {
		return nil, errors.New("mqtt: brokerUrl is required")
	}
	if len(cfg.Topics) == 0 {
		return nil, errors.New("mqtt: at least one topic is required")
	}
	if cfg.QoS < 0 || cfg.QoS > 2 {
		return nil, errors.New("mqtt: qos must be 0, 1 or 2")
	}
	if cfg.Framing != "" && cfg.Framing != "message" {
		return nil, errors.New("mqtt: framing must be \"message\"")
	}
	return &Subscriber{cfg: cfg}, nil
}

// Run connects (retrying), subscribes, and forwards messages until ctx ends.
func (s *Subscriber) Run(ctx context.Context, emit transport.Emit) error {
	clientID := s.cfg.ClientID
	if clientID == "" {
		clientID = fmt.Sprintf("sentinel-ingest-%d", time.Now().UnixNano())
	}
	lost := make(chan error, 1)
	opts := paho.NewClientOptions().
		AddBroker(s.cfg.BrokerURL).
		SetClientID(clientID).
		SetAutoReconnect(true).
		SetMaxReconnectInterval(maxBackoff).
		SetOrderMatters(false).
		SetCleanSession(true).
		SetConnectionLostHandler(func(_ paho.Client, err error) {
			s.Set(func(st *transport.State) { st.Connected = false; st.Connections = 0; st.LastError = err.Error() })
		}).
		SetOnConnectHandler(func(c paho.Client) {
			filters := make(map[string]byte, len(s.cfg.Topics))
			for _, t := range s.cfg.Topics {
				filters[t] = byte(s.cfg.QoS)
			}
			token := c.SubscribeMultiple(filters, func(_ paho.Client, m paho.Message) {
				f := transport.Frame{Payload: m.Payload(), ReceivedAt: time.Now().UTC(), Remote: m.Topic(), Attrs: map[string]string{"topic": m.Topic()}}
				_ = emit(ctx, f) // drop on backpressure; QoS redelivery is not wanted here
			})
			if !token.WaitTimeout(connectTimeout) || token.Error() != nil {
				err := token.Error()
				if err == nil {
					err = errors.New("subscribe timeout")
				}
				s.Set(func(st *transport.State) { st.LastError = "subscribe: " + err.Error() })
				return
			}
			s.Set(func(st *transport.State) { st.Connected = true; st.Connections = 1; st.LastError = "" })
		})
	if s.cfg.Username != "" {
		opts.SetUsername(s.cfg.Username).SetPassword(s.cfg.Password)
	}
	if tlsCfg, err := s.cfg.TLSConfig(); err != nil {
		return err
	} else if tlsCfg != nil {
		opts.SetTLSConfig(tlsCfg)
	}
	client := paho.NewClient(opts)

	backoff := time.Duration(0)
	for ctx.Err() == nil {
		token := client.Connect()
		if !token.WaitTimeout(connectTimeout) || token.Error() != nil {
			err := token.Error()
			if err == nil {
				err = errors.New("connect timeout")
			}
			s.Set(func(st *transport.State) { st.Connected = false; st.LastError = err.Error() })
			backoff = transport.Backoff(backoff, maxBackoff)
			if err := transport.Sleep(ctx, backoff); err != nil {
				return nil
			}
			continue
		}
		break
	}
	if ctx.Err() != nil {
		return nil
	}
	select {
	case <-ctx.Done():
	case err := <-lost:
		return err
	}
	client.Disconnect(1000)
	s.Set(func(st *transport.State) { st.Connected = false; st.Connections = 0 })
	return nil
}

// State reports connectivity.
func (s *Subscriber) State() transport.State { return s.Get() }
