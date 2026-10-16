package stomp

import (
	"testing"

	"github.com/sentinel/ingest-service/internal/source/transport"
)

func TestValidation(t *testing.T) {
	if _, err := New(transport.Config{Kind: "stomp", BrokerURL: "broker:61613", Queue: "/queue/x"}); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []transport.Config{
		{Kind: "stomp", Queue: "/queue/x"},
		{Kind: "stomp", BrokerURL: "broker:61613"},
		{Kind: "stomp", BrokerURL: "tcp://broker:61613", Queue: "/queue/x"},
		{Kind: "stomp", BrokerURL: "broker:61613", Queue: "/queue/x", Framing: "line"},
	} {
		if _, err := New(bad); err == nil {
			t.Errorf("%+v should be rejected", bad)
		}
	}
}
