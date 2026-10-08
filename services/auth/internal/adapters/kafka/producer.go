package kafkaadapter

import (
	"context"
	"fmt"
	"sort"

	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/zima-flow/go-mentor/mini-fintech/platform/outbox"
)

type Producer struct {
	client *kgo.Client
}

var _ outbox.Publisher = (*Producer)(nil)

func NewProducer(client *kgo.Client) *Producer { return &Producer{client: client} }

func (p *Producer) Publish(ctx context.Context, topic string, headers map[string]string, payload []byte) error {
	record := &kgo.Record{Topic: topic, Value: payload, Headers: recordHeaders(headers)}
	if err := p.client.ProduceSync(ctx, record).FirstErr(); err != nil {
		return fmt.Errorf("kafka: publish to %s: %w", topic, err)
	}
	return nil
}

func recordHeaders(headers map[string]string) []kgo.RecordHeader {
	if len(headers) == 0 {
		return nil
	}

	keys := make([]string, 0, len(headers))
	for key := range headers {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	out := make([]kgo.RecordHeader, 0, len(keys))
	for _, key := range keys {
		out = append(out, kgo.RecordHeader{Key: key, Value: []byte(headers[key])})
	}
	return out
}
