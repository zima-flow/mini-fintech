package kafka_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kgo"

	"github.com/zima-flow/go-mentor/mini-fintech/platform/kafka"
)

func TestNewProducer_RequiresBrokers(t *testing.T) {
	t.Parallel()

	_, err := kafka.NewProducer(kafka.Config{})
	require.Error(t, err)
}

func TestNewConsumer_RequiresBrokers(t *testing.T) {
	t.Parallel()

	_, err := kafka.NewConsumer(kafka.Config{GroupID: "auth"})
	require.Error(t, err)
}

func TestNewProducer_Constructs(t *testing.T) {
	t.Parallel()

	client, err := kafka.NewProducer(kafka.Config{Brokers: []string{"127.0.0.1:9092"}, ClientID: "auth"})
	require.NoError(t, err)
	t.Cleanup(client.Close)
}

func TestNewConsumer_ConstructsWithTopics(t *testing.T) {
	t.Parallel()

	client, err := kafka.NewConsumer(kafka.Config{
		Brokers: []string{"127.0.0.1:9092"},
		GroupID: "auth",
		Topics:  []string{"customer.events"},
	})
	require.NoError(t, err)
	t.Cleanup(client.Close)
}

func TestRecordHeaders_ConvertsAndHandlesEmpty(t *testing.T) {
	t.Parallel()

	require.Nil(t, kafka.RecordHeaders(nil))
	require.Nil(t, kafka.RecordHeaders(&kgo.Record{}))

	record := &kgo.Record{Headers: []kgo.RecordHeader{
		{Key: "traceparent", Value: []byte("00-abc-def-01")},
		{Key: "x-request-id", Value: []byte("req-1")},
	}}
	require.Equal(t, map[string]string{
		"traceparent":  "00-abc-def-01",
		"x-request-id": "req-1",
	}, kafka.RecordHeaders(record))
}
