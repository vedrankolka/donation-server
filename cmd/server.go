package main

import (
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/joho/godotenv"
	"github.com/stripe/stripe-go/v72"
	"github.com/vedrankolka/donation-server/pkg/handler"
	"github.com/vedrankolka/donation-server/pkg/notifier/kafka"
)

func main() {
	for _, envFile := range os.Args[1:] {
		if err := godotenv.Load(envFile); err != nil {
			log.Printf("Error loading %s: %v", envFile, err)
		}
	}

	// Stripe variables.
	publishableKey := os.Getenv("STRIPE_PUBLISHABLE_KEY")
	stripe.Key = os.Getenv("STRIPE_SECRET_KEY")
	webhookSecret := os.Getenv("STRIPE_WEBHOOK_SECRET")
	port := os.Getenv("DONATION_SERVER_PORT")
	// Kafka (Upstash) variables.
	bootstrapServers := os.Getenv("UPSTASH_KAFKA_BOOTSTRAP_SERVERS")
	customersTopic := os.Getenv("DONATION_SERVER_CUSTOMERS_TOPIC")
	kafkaUsername := os.Getenv("UPSTASH_KAFKA_SCRAM_USERNAME")
	kafkaPassword := os.Getenv("UPSTASH_KAFKA_SCRAM_PASSWORD")

	// For sample support and debugging, not required for production:
	stripe.SetAppInfo(&stripe.AppInfo{
		Name:    "stripe-samples/accept-a-payment/payment-element",
		Version: "0.0.1",
		URL:     "https://github.com/vedrankolka/donation-server",
	})

	// Kafka client for sending events about confirmed payments.
	notifier, err := kafka.NewKafkaNotifier(strings.Split(bootstrapServers, ","), customersTopic, kafkaUsername, kafkaPassword)
	if err != nil {
		log.Printf("Could not construct KafkaNotifier: %v\n", err)
		return
	}

	donationHandler, err := handler.NewHandler(publishableKey, webhookSecret, notifier)
	if err != nil {
		log.Fatalf("Could not create DonationHandler: %v", err)
	}

	http.HandleFunc("/config", donationHandler.HandleConfig)
	http.HandleFunc("/create-payment-intent", donationHandler.HandleCreatePaymentIntent)
	if bootstrapServers != "" {
		http.HandleFunc("/webhook", donationHandler.HandleWebhook)
	}

	log.Println("server running at 0.0.0.0:" + port)
	if err := http.ListenAndServe("0.0.0.0:"+port, nil); err != nil {
		log.Fatal(err)
	}
}
