FROM golang:1.25-alpine AS builder

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 go build -buildvcs=false -o /diu ./cmd/diu

FROM alpine:3.22

RUN apk add --no-cache bash zsh ca-certificates

COPY --from=builder /diu /usr/local/bin/diu

ENTRYPOINT ["diu"]
CMD ["--help"]
