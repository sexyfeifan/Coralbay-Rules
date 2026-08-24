FROM golang:1.23-alpine AS build
ARG VERSION=4.2.1
WORKDIR /src
COPY go.mod ./
COPY main.go ./
COPY cmd ./cmd
COPY web ./web
RUN normalized="${VERSION#v}" && CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${normalized}" -o /out/coralbay-rules .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/coralbay-ruleconvert ./cmd/ruleconvert

FROM alpine:3.22
ARG VERSION=4.2.1
LABEL org.opencontainers.image.title="CoralBay Rules" \
      org.opencontainers.image.source="https://github.com/sexyfeifan/Coralbay-Rules" \
      org.opencontainers.image.version="${VERSION}"
RUN apk add --no-cache ca-certificates curl git tzdata

COPY expected-files.txt /app/expected-files.txt
COPY templates /app/templates
COPY assets /app/assets
COPY sync.sh /usr/local/bin/coralbay-rules-sync
COPY --from=build /out/coralbay-rules /usr/local/bin/coralbay-rules
COPY --from=build /out/coralbay-ruleconvert /usr/local/bin/coralbay-ruleconvert

RUN chmod 0755 /usr/local/bin/coralbay-rules-sync /usr/local/bin/coralbay-rules /usr/local/bin/coralbay-ruleconvert

VOLUME ["/data"]
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/coralbay-rules"]
ENV GENERATOR_VERSION=${VERSION}
