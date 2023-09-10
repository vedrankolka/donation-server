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
	"github.com/vedrankolka/donation-server/pkg/notifier"
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
	notifier       notifier.Notifier
}

const (
	Currency = "EUR"
	Timeout  = 2 * time.Second
)

func NewHandler(publishableKey, webhookSecret string, notifier notifier.Notifier) (*DonationHandler, error) {
	if publishableKey == "" {
		return nil, errors.New("a publishableKey cannot be empty.")
	}

	if webhookSecret == "" {
		log.Println("[WARN] webhookSecret is not set.")
	}

	return &DonationHandler{
		publishableKey: publishableKey,
		webhookSecret:  webhookSecret,
		stripeClient:   client.New(stripe.Key, nil),
		notifier:       notifier,
	}, nil
}

// HandleConfig returns the public key for creating a PaymentIntent.
func (dh *DonationHandler) HandleConfig(w http.ResponseWriter, r *http.Request) {
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

// HandleWebhook handles an event of a completed checkout.
func (dh *DonationHandler) HandleWebhook(w http.ResponseWriter, r *http.Request) {
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

	if event.Type != "charge.succeeded" {
		log.Printf("This webhook handles charge.succeeded, but got %q\n", event.Type)
	} else {
		log.Println("charge.succeeded!")

		// Get the customer if it exists.
		customer, err := dh.getCustomer(event)
		if err != nil {
			log.Printf("Could not fetch customer received event: %v\n", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		// If the customer does not exist, create it.
		if customer == nil {
			customer, err = dh.createCustomer(event)
			if err != nil {
				log.Printf("Could not create customer: %v", err)
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}

			log.Printf("Created new customer with id %q and email %q\n", customer.ID, customer.Email)
		} else {
			log.Printf("Found existing customer with id %q and email %q\n", customer.ID, customer.Email)
		}

		donationEvent := notifier.DonationEvent{
			CustomerID:    customer.ID,
			CustomerName:  customer.Name,
			CustomerEmail: customer.Email,
			Amount:        event.Data.Object["amount"].(float64),
			Currency:      event.Data.Object["currency"].(string),
		}

		ctx, cancel := context.WithTimeout(r.Context(), Timeout)
		defer cancel()

		if err := dh.notifier.Notify(ctx, donationEvent); err != nil {
			log.Printf("Failed to notify about donation: %v\n", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
	}

	dh.writeJSON(w, nil)
}

func (dh *DonationHandler) createCustomer(event stripe.Event) (*stripe.Customer, error) {
	billingDetails, ok := event.Data.Object["billing_details"].(map[string]interface{})
	if !ok {
		return nil, errors.New(fmt.Sprintf("Could not read billing_details: %v", billingDetails))
	}

	email, ok := billingDetails["email"].(string)
	if !ok {
		return nil, errors.New("Email could not be read from billing_details.")
	}
	name, ok := billingDetails["name"].(string)
	if !ok {
		return nil, errors.New("Name could not be read from billing_details.")
	}

	if email == "" || name == "" {
		return nil, errors.New("Cannot create customer with no email address and name.")
	}

	return dh.stripeClient.Customers.New(&stripe.CustomerParams{
		Email: stripe.String(email),
		Name:  stripe.String(name),
	})
}

func (dh *DonationHandler) getCustomer(event stripe.Event) (*stripe.Customer, error) {
	var customer *stripe.Customer
	// Try to get the customer by ID.
	customerId, ok := event.Data.Object["customer"].(string)
	if ok {
		var err error
		customer, err = dh.stripeClient.Customers.Get(customerId, &stripe.CustomerParams{})
		if err != nil {
			return nil, errors.New(fmt.Sprintf("Could not fetch customer by ID %q", customerId))
		}
	} else {
		// Try to get customer by email.
		billingDetails, ok := event.Data.Object["billing_details"].(map[string]interface{})
		if !ok {
			return nil, errors.New("Could not read billing_detials from event.")
		}

		email, ok := billingDetails["email"].(string)
		if !ok {
			return nil, errors.New("Could not read email from billing_details.")
		}

		iter := dh.stripeClient.Customers.List(&stripe.CustomerListParams{
			Email: stripe.String(email),
		})

		if iter.Err() != nil {
			return nil, errors.New(fmt.Sprintf("Could not fetch customers by email %q: %v", email, iter.Err()))
		}

		customerList := iter.CustomerList().Data
		if len(customerList) == 0 {
			return nil, nil
		} else if len(customerList) == 1 {
			return customerList[0], nil
		} else {
			// Find the first on with the entered name.
			name, ok := billingDetails["email"].(string)
			if !ok {
				return nil, errors.New("Could not read name from billing_details.")
			}

			for _, c := range customerList {
				if c.Name == name {
					return c, nil
				}
			}
			// If no customer matched by name,
			// conclude email is enough and return the first one.
			return customerList[0], nil
		}
	}

	return customer, nil
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
