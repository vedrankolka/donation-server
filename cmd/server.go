package main

import (
	"log"
	"net/http"
	"os"
	"strings"

	"github.com/joho/godotenv"
	"github.com/stripe/stripe-go/v72"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/vedrankolka/donation-server/pkg/handler"
)

func main() {
	for _, envFile := range os.Args[1:] {
		if err := godotenv.Load(envFile); err != nil {
			log.Printf("Error loading %s: %v", envFile, err)
		}
	}

	publishableKey := os.Getenv("STRIPE_PUBLISHABLE_KEY")
	stripe.Key = os.Getenv("STRIPE_SECRET_KEY")
	webhookSecret := os.Getenv("STRIPE_WEBHOOK_SECRET")
	port := os.Getenv("PORT")
	customersTopic := os.Getenv("CUSTOMERS_TOPIC")

	// For sample support and debugging, not required for production:
	stripe.SetAppInfo(&stripe.AppInfo{
		Name:    "stripe-samples/accept-a-payment/payment-element",
		Version: "0.0.1",
		URL:     "https://github.com/stripe-samples",
	})

	// Kafka client for sending events about confirmed payments.
	bootstrapServers := os.Getenv("BOOTSTRAP_SERVERS")
	var kafkaClient *kgo.Client
	if bootstrapServers != "" {
		var err error
		kafkaClient, err = kgo.NewClient(
			kgo.SeedBrokers(strings.Split(bootstrapServers, ",")...),
			// TODO setup authentication to Upstash
			// kgo.SASL(scram.Sha512())
		)
		if err != nil {
			log.Printf("Failed to construct kafkaClient: %v", err)
		}
		defer kafkaClient.Close()
	}

	donationHandler, err := handler.NewHandler(publishableKey, webhookSecret, customersTopic, kafkaClient)
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
