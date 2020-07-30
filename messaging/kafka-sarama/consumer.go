package kafka_sarama

import (
	"context"
	"github.com/Shopify/sarama"
	"github.com/delanri/commonutil/messaging"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"
)

func (l *Kafka) AddTopicListener(topic string, callback messaging.CallbackFunc) {
	l.mu.Lock()
	defer func() {
		l.mu.Unlock()
	}()
	functions := l.CallbackFunctions[topic]
	functions = append(functions, callback)
	l.CallbackFunctions[topic] = functions
	l.Option.ListTopics = append(l.Option.ListTopics, topic)
}

func (l *Kafka) Listen() {

	consumer := Consumer{
		ready:             make(chan bool),
		CallbackFunctions: l.CallbackFunctions,
		Option:            l.Option,
	}

	ctx, cancel := context.WithCancel(context.Background())
	listener, err := l.NewListener()
	if err != nil {
		return
	}
	l.Consumer = listener

	wg := &sync.WaitGroup{}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			// `Consume` should be called inside an infinite loop, when a
			// server-side rebalance happens, the consumer session will need to be
			// recreated to get the new claims
			err := l.Consumer.Consume(ctx, l.Option.ListTopics, &consumer)
			if err != nil {
				log.Panicf("Error from consumer: %v", err)
			}
			// check if context was cancelled, signaling that the consumer should stop
			if ctx.Err() != nil {
				return
			}
			consumer.ready = make(chan bool)
		}
	}()

	<-consumer.ready // Await till the consumer has been set up
	log.Printf("Sarama consumer up and running! Listening to  brokers %s \n", l.Option.Host)

	sigterm := make(chan os.Signal, 1)
	signal.Notify(sigterm, syscall.SIGINT, syscall.SIGTERM)
	select {
	case <-ctx.Done():
		log.Println("terminating: context cancelled")
	case <-sigterm:
		log.Println("terminating: via signal")
	}

	cancel()
	wg.Wait()
	if err := l.Consumer.Close(); err != nil {
		log.Panicf("Error closing client: %v", err)
	}
}

// Consumer represents a Sarama consumer group consumer
type Consumer struct {
	ready             chan bool
	CallbackFunctions map[string][]messaging.CallbackFunc
	Option            *Option
}

// Setup is run at the beginning of a new session, before ConsumeClaim
func (consumer *Consumer) Setup(sarama.ConsumerGroupSession) error {
	// Mark the consumer as ready
	close(consumer.ready)
	return nil
}

// Cleanup is run at the end of a session, once all ConsumeClaim goroutines have exited
func (consumer *Consumer) Cleanup(sarama.ConsumerGroupSession) error {
	return nil
}

// ConsumeClaim must start a consumer loop of ConsumerGroupClaim's Messages().
func (consumer *Consumer) ConsumeClaim(session sarama.ConsumerGroupSession, claim sarama.ConsumerGroupClaim) error {

	// NOTE:
	// Do not move the code below to a goroutine.
	// The `ConsumeClaim` itself is called within a goroutine, see:
	// https://github.com/Shopify/sarama/blob/master/consumer_group.go#L27-L29
	for message := range claim.Messages() {
		session.MarkMessage(message, "")
		for _, callback := range consumer.CallbackFunctions[message.Topic] {
			err := callback(message.Value)
			if err != nil {
				consumer.Option.Log.Error(err)
			}
		}
	}

	return nil
}
