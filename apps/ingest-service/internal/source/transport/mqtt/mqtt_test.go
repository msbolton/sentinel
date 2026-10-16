package mqtt

import (
	"testing"

	"github.com/sentinel/ingest-service/internal/source/transport"
)

func TestValidation(t *testing.T) {
	good := transport.Config{Kind: "mqtt", BrokerURL: "tcp://broker:1883", Topics: []string{"a/#"}, QoS: 1}
	if _, err := New(good); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []transport.Config{
		{Kind: "mqtt", Topics: []string{"a"}},
		{Kind: "mqtt", BrokerURL: "tcp://b"},
		{Kind: "mqtt", BrokerURL: "tcp://b", Topics: []string{"a"}, QoS: 3},
		{Kind: "mqtt", BrokerURL: "tcp://b", Topics: []string{"a"}, Framing: "line"},
	} {
		if _, err := New(bad); err == nil {
			t.Errorf("%+v should be rejected", bad)
		}
	}
}
