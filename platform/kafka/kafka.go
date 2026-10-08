package kafka

import (
	"fmt"

	"github.com/twmb/franz-go/pkg/kgo"
)

type Config struct {
	Brokers  []string
	ClientID string
	GroupID  string
	Topics   []string
}

func baseOpts(cfg Config) ([]kgo.Opt, error) {
	if len(cfg.Brokers) == 0 {
		return nil, fmt.Errorf("kafka: at least one broker is required")
	}

	opts := []kgo.Opt{kgo.SeedBrokers(cfg.Brokers...)}
	if cfg.ClientID != "" {
		opts = append(opts, kgo.ClientID(cfg.ClientID))
	}

	return opts, nil
}

func NewProducer(cfg Config) (*kgo.Client, error) {
	opts, err := baseOpts(cfg)
	if err != nil {
		return nil, err
	}

	return kgo.NewClient(opts...)
}

func NewConsumer(cfg Config) (*kgo.Client, error) {
	opts, err := baseOpts(cfg)
	if err != nil {
		return nil, err
	}

	opts = append(opts, kgo.ConsumerGroup(cfg.GroupID))
	if len(cfg.Topics) > 0 {
		opts = append(opts, kgo.ConsumeTopics(cfg.Topics...))
	}
	return kgo.NewClient(opts...)
}

func RecordHeaders(record *kgo.Record) map[string]string {
	if record == nil || len(record.Headers) == 0 {
		return nil
	}
	headers := make(map[string]string, len(record.Headers))
	for _, h := range record.Headers {
		headers[h.Key] = string(h.Value)
	}
	return headers
}
