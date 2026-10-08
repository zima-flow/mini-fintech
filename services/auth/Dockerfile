FROM golang:1.27 AS build

WORKDIR /src

COPY go.work go.work.sum ./
COPY contracts/ ./contracts/
COPY platform/ ./platform/
COPY services/auth/ ./services/auth/

RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" \
    -o /out/auth ./services/auth/cmd/auth

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/auth /auth

EXPOSE 50051 8081

USER nonroot:nonroot
ENTRYPOINT ["/auth"]
