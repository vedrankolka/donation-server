FROM golang:1.18.1-alpine AS build

RUN apk add --no-cache git

WORKDIR /donation-server
ADD cmd cmd
ADD pkg pkg
ADD go.mod .
ADD go.sum .

# TODO remove
RUN ls -la

RUN go mod download
RUN go build -o server ./cmd/server.go

FROM alpine:latest

COPY --from=build /donation-server/server /app/server

EXPOSE 8080
ENTRYPOINT [ "/app/server" ]
