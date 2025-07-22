FROM ghcr.io/blinklabs-io/go:1.24.4-1 AS build

WORKDIR /code
COPY . .
RUN make build

FROM cgr.dev/chainguard/glibc-dynamic AS indexer
COPY --from=build /code/indexer /bin/

VOLUME /data
WORKDIR /data
ENTRYPOINT ["indexer"]
