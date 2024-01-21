# Accept a donation

A Go implementation for handling donations from the donation page.

This repo was forked from the Stripe quickstarts.
You can [🎥 watch a video](https://www.youtube.com/watch?v=cbsCxLDL4EY) to see how the quickstart server was implemented.

## Requirements

- Go 1.13
- Configured `.env` file
- Kafka cluster (if the webhook should send donation notifications to it)
- Fly.io CLI and account set up (for deploying only)
- Docker (for deploying only)

## How to run

1. Confirm `.env` configuration

Ensure the API keys are configured in `.env` in this directory. It should include the following keys:

```sh
# Stripe API keys - see https://stripe.com/docs/development/quickstart#api-keys
STRIPE_PUBLISHABLE_KEY=pk_test...
STRIPE_SECRET_KEY=sk_test...

# Required to verify signatures in the webhook handler.
# See README on how to use the Stripe CLI to test webhooks
STRIPE_WEBHOOK_SECRET=whsec_...

# Port on which the server is exposed and Kafka topic name on which notifications are sent.
DONATION_SERVER_PORT="8080"
DONATION_SERVER_CUSTOMERS_TOPIC="customers"

# Other Kafka related variables.
UPSTASH_KAFKA_BOOTSTRAP_SERVERS=localhost:9092
UPSTASH_KAFKA_SCRAM_USERNAME=...
UPSTASH_KAFKA_SCRAM_PASSWORD=...
```

2. Install dependencies

From the root of the project run:

```sh
go mod tidy
go mod vendor
```

3. Run the application

Again from the root of the project, run:

```sh
go run cmd/server.go .env
```

## How to deploy to Fly.io
[Fly.io](https://fly.io) offers an easy (and free for 2 small machines) way to deploy apps using
a [`Dockerfile`](./Dockerfile) and a [`fly.toml`](./fly.toml).
To build the docker image on your own machine (because building on Fly isn't free)
and deploy to one of the free machines, run:

```sh
fly deploy --vm-size shared-cpu-1x --local-only
```

The `vm-size` specifies the small (free) machine, while `local-only` flag specifies that the image should be built locally,
which means Docker should be running.

## TODO

- [x] rewrite README
- [x] extract some variables as constants
- [ ] move customer creation out of the Webhook, but where? To another webhook? It might stay as is.
- [x] wrap http handlers to allow CORS

## Note to self

You have the whole thing documented on your tablet.
