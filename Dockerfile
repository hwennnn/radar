FROM golang:1.26 AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/radar ./cmd/radar

FROM gcr.io/distroless/static-debian12:nonroot

WORKDIR /app
COPY --from=build /out/radar /app/radar
COPY --from=build /src/config /app/config
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/app/radar"]
CMD ["routine"]
