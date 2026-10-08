package kafkaadapter

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/twmb/franz-go/pkg/kgo"
)

func TestRecordHeaders_SortedAndConverted(t *testing.T) {
	t.Parallel()

	got := recordHeaders(map[string]string{
		"x-request-id": "req-1",
		"traceparent":  "00-trace",
	})
	require.Len(t, got, 2)
	require.Equal(t, "traceparent", got[0].Key)
	require.Equal(t, []byte("00-trace"), got[0].Value)
	require.Equal(t, "x-request-id", got[1].Key)
	require.Equal(t, []byte("req-1"), got[1].Value)
}

func TestRecordHeaders_Empty(t *testing.T) {
	t.Parallel()

	require.Nil(t, recordHeaders(nil))
	require.Nil(t, recordHeaders(map[string]string{}))
}

func TestProducer_Publish_ReturnsError(t *testing.T) {
	t.Parallel()

	client, err := kgo.NewClient(kgo.SeedBrokers("127.0.0.1:1"))
	require.NoError(t, err)
	client.Close()

	producer := NewProducer(client)
	err = producer.Publish(context.Background(), "auth.events", map[string]string{"traceparent": "x"}, []byte("payload"))
	require.Error(t, err)
	require.True(t, errors.Is(err, kgo.ErrClientClosed), "want the client-closed error, got %v", err)
}
