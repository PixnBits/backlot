# Session router — ordinary container. No /dev/kvm. No privileged.
# Without LOT_BOSS_URL, leases return 503 world engine unavailable.
FROM golang:1.22-bookworm AS build
WORKDIR /src
COPY go.mod ./
COPY . .
RUN CGO_ENABLED=0 go build -o /router ./cmd/router

FROM debian:bookworm-slim
RUN useradd -u 10002 -r -s /usr/sbin/nologin router
COPY --from=build /router /usr/local/bin/router
USER router
ENV ROUTER_ADDR=:8080 DESK_URL=http://desk:8090
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/router"]
