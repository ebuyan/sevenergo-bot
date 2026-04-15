FROM golang:1.26-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o energo .

FROM alpine:3.21

COPY --from=builder /app/energo /energo

EXPOSE 8080

ENTRYPOINT ["/energo"]
