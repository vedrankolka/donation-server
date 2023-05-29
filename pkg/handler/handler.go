package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/ioutil"
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/stripe/stripe-go/v72"
	"github.com/stripe/stripe-go/v72/client"
	"github.com/stripe/stripe-go/v72/paymentintent"
	"github.com/stripe/stripe-go/v72/webhook"
	"github.com/twmb/franz-go/pkg/kgo"
)

// ErrorResponseMessage represents the structure of the error
// object sent in failed responses.
type ErrorResponseMessage struct {
	Message string `json:"message"`
}

// ErrorResponse represents the structure of the error object sent
// in failed responses.
type ErrorResponse struct {
	Error *ErrorResponseMessage `json:"error"`
}

type DonationHandler struct {
	publishableKey string
	webhookSecret  string
	stripeClient   *client.API
	kafkaClient    *kgo.Client
	customerTopic  string
}

const (
	Currency = "EUR"
	Timeout  = 500 * time.Millisecond
)

func NewHandler(publishableKey, webhookSecret, customerTopic string, kafkaClient *kgo.Client) (*DonationHandler, error) {
	if publishableKey == "" {
		return nil, errors.New("a publishableKey cannot be empty.")
	}

	if webhookSecret == "" {
		log.Println("[WARN] webhookSecret is not set.")
	}

	return &DonationHandler{
		publishableKey: publishableKey,
		webhookSecret:  webhookSecret,
		customerTopic:  customerTopic,
		stripeClient:   client.New(stripe.Key, nil),
		kafkaClient:    kafkaClient,
	}, nil
}

// enableCors enables CORS.
func (dh *DonationHandler) enableCors(w *http.ResponseWriter) {
	(*w).Header().Set("Access-Control-Allow-Origin", "*")
}

// HandleConfig returns the public key for creating a PaymentIntent.
func (dh *DonationHandler) HandleConfig(w http.ResponseWriter, r *http.Request) {
	dh.enableCors(&w)
	log.Println("/config called.")
	if r.Method != "GET" {
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		return
	}
	dh.writeJSON(w, struct {
		PublishableKey string `json:"publishableKey"`
	}{
		PublishableKey: dh.publishableKey,
	})
}

// HandleCreatePaymentIntent creates a payment intent.
func (dh *DonationHandler) HandleCreatePaymentIntent(w http.ResponseWriter, r *http.Request) {
	dh.enableCors(&w)
	amount, err := getAmount(r)
	if err != nil || amount < 1 {
		log.Printf("Amount was not set correctly %v\n", err)
		return
	}

	log.Printf("amount = %d\n", amount)

	params := &stripe.PaymentIntentParams{
		Amount:   stripe.Int64(amount),
		Currency: stripe.String(Currency),
		AutomaticPaymentMethods: &stripe.PaymentIntentAutomaticPaymentMethodsParams{
			Enabled: stripe.Bool(true),
		},
	}

	pi, err := paymentintent.New(params)
	if err != nil {
		// Try to safely cast a generic error to a stripe.Error so that we can get at
		// some additional Stripe-specific information about what went wrong.
		if stripeErr, ok := err.(*stripe.Error); ok {
			fmt.Printf("Other Stripe error occurred: %v\n", stripeErr.Error())
			dh.writeJSONErrorMessage(w, stripeErr.Error(), 400)
		} else {
			fmt.Printf("Other error occurred: %v\n", err.Error())
			dh.writeJSONErrorMessage(w, "Unknown server error", 500)
		}

		return
	}

	dh.writeJSON(w, struct {
		ClientSecret string `json:"clientSecret"`
	}{
		ClientSecret: pi.ClientSecret,
	})
}

func (dh *DonationHandler) HandleWebhook(w http.ResponseWriter, r *http.Request) {
	dh.enableCors(&w)
	log.Println("Webhook is called.")
	if r.Method != "POST" {
		http.Error(w, http.StatusText(http.StatusMethodNotAllowed), http.StatusMethodNotAllowed)
		log.Printf("Tried to access with %q method", r.Method)
		return
	}

	b, err := ioutil.ReadAll(r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		log.Printf("ioutil.ReadAll: %v", err)
		return
	}

	event, err := webhook.ConstructEvent(b, r.Header.Get("Stripe-Signature"), dh.webhookSecret)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		log.Printf("webhook.ConstructEvent: %v", err)
		return
	}

	if event.Type == "checkout.session.completed" {
		log.Println("Checkout Session completed!")
		customerId, ok := event.Data.Object["customer"].(string)
		if !ok {
			log.Printf("Failed to read customer id: %v", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}

		customer, err := dh.stripeClient.Customers.Get(customerId, &stripe.CustomerParams{})
		if err != nil {
			log.Printf("could not fetch customer with id %q: %v", customerId, err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		data, err := json.Marshal(customer)
		if err != nil {
			log.Printf("Could not marshal customer %q: %v", customer.ID, err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		record := kgo.Record{
			Key:   []byte(customer.ID),
			Value: data,
			Topic: dh.customerTopic,
		}

		ctx, cancel := context.WithTimeout(r.Context(), Timeout)
		defer cancel()
		result := dh.kafkaClient.ProduceSync(ctx, &record)
		if result == nil {
			log.Printf("Result of producing to %q is nil!\n", dh.customerTopic)
			http.Error(w, "Could not produce to customer topic.", http.StatusInternalServerError)
			return
		}
		sent, err := result.First()
		if result.FirstErr(); err != nil {
			log.Printf("Failed to produce record: %v", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		} else {
			log.Printf("Sent with offset: %d", sent.Offset)
		}
	}

	dh.writeJSON(w, nil)
}

func (dh *DonationHandler) writeJSON(w http.ResponseWriter, v interface{}) {
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(v); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		log.Printf("json.NewEncoder.Encode: %v", err)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if _, err := io.Copy(w, &buf); err != nil {
		log.Printf("io.Copy: %v", err)
		return
	}
}

func (dh *DonationHandler) writeJSONError(w http.ResponseWriter, v interface{}, code int) {
	w.WriteHeader(code)
	dh.writeJSON(w, v)
	return
}

func (dh *DonationHandler) writeJSONErrorMessage(w http.ResponseWriter, message string, code int) {
	resp := &ErrorResponse{
		Error: &ErrorResponseMessage{
			Message: message,
		},
	}
	dh.writeJSONError(w, resp, code)
}

func getAmount(r *http.Request) (int64, error) {
	amounts, ok := r.URL.Query()["amount"]
	if !ok || len(amounts) < 1 {
		return 0, errors.New("missing amount query parameter")
	}

	if len(amounts) > 1 {
		return 0, errors.New("more than one amount is specified")
	}

	return strconv.ParseInt(amounts[0], 10, 64)
}
