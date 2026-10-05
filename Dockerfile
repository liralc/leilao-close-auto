FROM golang:1.20 AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 go build -o /app/auction cmd/auction/main.go

FROM alpine:3.20

WORKDIR /app

COPY --from=builder /app/auction /app/auction
COPY --from=builder /app/cmd/auction/.env /app/cmd/auction/.env

EXPOSE 8080

ENTRYPOINT ["/app/auction"]
