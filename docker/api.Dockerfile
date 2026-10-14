FROM golang:1.27.2-bookworm AS build
WORKDIR /src
COPY api/build/server/go.mod api/build/server/go.sum ./
RUN go mod download
COPY api/build/server/*.go ./
RUN CGO_ENABLED=0 go build -trimpath -o /out/nba-api .

FROM debian:bookworm-slim
RUN apt-get update \
    && apt-get install -y --no-install-recommends ca-certificates \
    && rm -rf /var/lib/apt/lists/*
COPY --from=build /out/nba-api /usr/local/bin/nba-api
USER 10001:10001
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/nba-api"]
