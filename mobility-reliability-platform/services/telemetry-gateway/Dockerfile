FROM golang:1.26.5-bookworm AS build

WORKDIR /workspace/services/telemetry-gateway
COPY services/telemetry-gateway/go.mod services/telemetry-gateway/go.sum ./
RUN go mod download
COPY services/telemetry-gateway/cmd ./cmd
COPY services/telemetry-gateway/internal ./internal
COPY packages/contracts/fixtures /workspace/packages/contracts/fixtures
RUN CGO_ENABLED=0 GOOS=linux go test -mod=readonly ./... && \
    CGO_ENABLED=0 GOOS=linux go build -mod=readonly -trimpath -ldflags="-s -w" -o /out/telemetry-gateway ./cmd/server

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/telemetry-gateway /telemetry-gateway
EXPOSE 8080
ENTRYPOINT ["/telemetry-gateway"]
