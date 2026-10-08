FROM golang:1.27 AS build

WORKDIR /src

COPY go.work go.work.sum ./
COPY contracts/ ./contracts/
COPY platform/ ./platform/
COPY services/example/ ./services/example/

RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" \
    -o /out/example ./services/example/cmd/example

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/example /example

EXPOSE 50059

USER nonroot:nonroot
ENTRYPOINT ["/example"]
