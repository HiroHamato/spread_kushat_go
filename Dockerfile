FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /bin/spreadbot ./cmd/spreadbot

FROM alpine:3.20
RUN addgroup -S app && adduser -S app -G app
WORKDIR /app
COPY --from=builder /bin/spreadbot /usr/local/bin/spreadbot
USER app
ENV HOST=0.0.0.0
ENV PORT=3000
EXPOSE 3000
CMD ["/usr/local/bin/spreadbot"]
