FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod ./
COPY main.go ./
COPY web ./web
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/coralbay-rules .

FROM alpine:3.22
RUN apk add --no-cache ca-certificates curl git tzdata

COPY expected-files.txt /app/expected-files.txt
COPY templates /app/templates
COPY sync.sh /usr/local/bin/coralbay-rules-sync
COPY --from=build /out/coralbay-rules /usr/local/bin/coralbay-rules

RUN chmod 0755 /usr/local/bin/coralbay-rules-sync /usr/local/bin/coralbay-rules

VOLUME ["/data"]
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/coralbay-rules"]
