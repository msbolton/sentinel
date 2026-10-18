package density

import (
	"testing"

	"github.com/confluentinc/confluent-kafka-go/v2/kafka"
	"google.golang.org/protobuf/proto"

	sentinelv1 "github.com/sentinel/proto-gen/sentinel/v1"
)

type memProducer struct{ msgs []*kafka.Message }

func (m *memProducer) Produce(msg *kafka.Message) error {
	m.msgs = append(m.msgs, msg)
	return nil
}

func TestGridsCarryTheFeedAndItsClassification(t *testing.T) {
	p := &memProducer{}
	pub := New(p, "density.events")

	if err := pub.For("aisstream", "").PublishDensity(&sentinelv1.DensityGrid{Total: 3}); err != nil {
		t.Fatal(err)
	}
	if err := pub.For("secret-feed", "SECRET").PublishDensity(&sentinelv1.DensityGrid{Classification: sentinelv1.Classification_CLASSIFICATION_CONFIDENTIAL}); err != nil {
		t.Fatal(err)
	}
	if err := pub.For("ts-feed", "SECRET").PublishDensity(&sentinelv1.DensityGrid{Classification: sentinelv1.Classification_CLASSIFICATION_TOP_SECRET}); err != nil {
		t.Fatal(err)
	}

	want := []struct {
		key   string
		level sentinelv1.Classification
	}{
		{"aisstream", sentinelv1.Classification_CLASSIFICATION_UNCLASSIFIED},
		{"secret-feed", sentinelv1.Classification_CLASSIFICATION_SECRET},  // raised to the feed's level
		{"ts-feed", sentinelv1.Classification_CLASSIFICATION_TOP_SECRET}, // never lowered
	}
	for i, w := range want {
		m := p.msgs[i]
		if *m.TopicPartition.Topic != "density.events" || string(m.Key) != w.key {
			t.Errorf("message %d: topic %s key %s", i, *m.TopicPartition.Topic, m.Key)
		}
		var g sentinelv1.DensityGrid
		if err := proto.Unmarshal(m.Value, &g); err != nil {
			t.Fatal(err)
		}
		if g.FeedId != w.key || g.Classification != w.level {
			t.Errorf("message %d: grid %+v", i, &g)
		}
	}
}
