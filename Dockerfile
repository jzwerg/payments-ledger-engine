# Go API image for the payment/ledger service.
# Multi-stage: build a static binary, ship it on a minimal runtime.
# `golang:1-bookworm` tracks the latest stable Go 1.x, matching CI (go-version: stable).

FROM golang:1-bookworm AS build
WORKDIR /src

# Cache dependencies first.
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/api .

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /
COPY --from=build /out/api /api
EXPOSE 8080
ENTRYPOINT ["/api"]
