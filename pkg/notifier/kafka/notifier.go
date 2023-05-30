package kafka

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log"

	"github.com/segmentio/kafka-go"
	"github.com/segmentio/kafka-go/sasl/scram"
	"github.com/vedrankolka/donation-server/pkg/notifier"
)

type KafkaNotifier struct {
	writer kafka.Writer
}

func (kn *KafkaNotifier) Notify(ctx context.Context, event notifier.DonationEvent) error {
	data, err := json.Marshal(event)
	if err != nil {
		return errors.New(fmt.Sprintf("Could not marshal given event %v: %v", event, err))
	}

	return kn.writer.WriteMessages(ctx, kafka.Message{
		Key:   []byte(event.CustomerID),
		Value: data,
	})
}

func (kn *KafkaNotifier) Close() error {
	return kn.writer.Close()
}

func NewKafkaNotifier(bootstrapServers []string, topic, username, password string) (*KafkaNotifier, error) {
	log.Println("bootstrapServers: ", bootstrapServers)
	log.Println("topic: ", topic)
	log.Println("username: ", username)
	log.Println("password: ", password)

	var dialer *kafka.Dialer
	if username == "" && password == "" {
		dialer = kafka.DefaultDialer
	} else {
		scramMechanism, err := scram.Mechanism(scram.SHA256, username, password)
		if err != nil {
			return nil, err
		}
		dialer = &kafka.Dialer{
			SASLMechanism: scramMechanism,
			TLS:           &tls.Config{},
		}
	}
	config := kafka.WriterConfig{
		Brokers:   bootstrapServers,
		Topic:     topic,
		Dialer:    dialer,
		BatchSize: 1,
	}

	return &KafkaNotifier{*kafka.NewWriter(config)}, nil
}
