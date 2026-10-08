FROM golang:1.27 AS build

WORKDIR /src

COPY go.work go.work.sum ./
COPY contracts/ ./contracts/
COPY platform/ ./platform/
COPY services/api-gateway/ ./services/api-gateway/

RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" \
    -o /out/api-gateway ./services/api-gateway/cmd/api-gateway

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/api-gateway /api-gateway

EXPOSE 8080

USER nonroot:nonroot
ENTRYPOINT ["/api-gateway"]
