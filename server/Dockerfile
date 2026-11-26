# Relay OAuth Karta — build statik, tanpa CGO.
# Build:  docker build -t karta-oauth-relay .
# Run:    docker run -p 8080:8080 \
#           -e GOOGLE_OAUTH_CLIENT_ID=... -e GOOGLE_OAUTH_CLIENT_SECRET=... \
#           karta-oauth-relay

FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY *.go ./
ENV CGO_ENABLED=0
RUN go vet ./... && go test ./... && go build -trimpath -ldflags="-s -w" -o /out/relay-oauth .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /out/relay-oauth /relay-oauth
EXPOSE 8080
USER nonroot:nonroot
ENTRYPOINT ["/relay-oauth"]
