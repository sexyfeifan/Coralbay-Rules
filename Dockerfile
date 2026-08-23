FROM alpine:3.22

RUN apk add --no-cache ca-certificates curl git tzdata

COPY expected-files.txt /app/expected-files.txt
COPY templates /app/templates
COPY sync.sh /usr/local/bin/coralbay-rules-sync

RUN chmod 0755 /usr/local/bin/coralbay-rules-sync

VOLUME ["/data"]
ENTRYPOINT ["/usr/local/bin/coralbay-rules-sync"]
