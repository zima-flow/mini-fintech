FROM golang:1.27 AS build

WORKDIR /src

COPY go.work go.work.sum ./
COPY contracts/ ./contracts/
COPY platform/ ./platform/
COPY services/customer/ ./services/customer/

RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" \
    -o /out/customer ./services/customer/cmd/customer

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/customer /customer

EXPOSE 50052

USER nonroot:nonroot
ENTRYPOINT ["/customer"]
