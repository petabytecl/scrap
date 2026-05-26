FROM golang:1.26-alpine AS builder

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o /scrapd ./cmd/scrapd

FROM alpine:3.21

RUN addgroup -S scrap && adduser -S scrap -G scrap
RUN mkdir -p /data && chown scrap:scrap /data

COPY --from=builder /scrapd /usr/local/bin/scrapd

USER scrap

EXPOSE 9090 9091 9100

ENTRYPOINT ["/usr/local/bin/scrapd"]
