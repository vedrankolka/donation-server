package notifier

import (
	"context"
)

type DonationEvent struct {
	CustomerID    string  `json:"customerID"`
	CustomerName  string  `json:"customerName"`
	CustomerEmail string  `json:"customerEmail"`
	Amount        float64 `json:"amount"`
	Currency      string  `json:"currency"`
}

type Notifier interface {
	Notify(ctx context.Context, event DonationEvent) error
	Close() error
}
