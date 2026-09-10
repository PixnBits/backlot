# Continuity desk — ordinary container. No /dev/kvm. No privileged.
FROM golang:1.22-bookworm AS build
WORKDIR /src
COPY go.mod ./
COPY . .
RUN CGO_ENABLED=0 go build -o /desk ./cmd/desk

FROM debian:bookworm-slim
RUN useradd -u 10001 -r -s /usr/sbin/nologin desk \
 && mkdir -p /data && chown desk:desk /data
COPY --from=build /desk /usr/local/bin/desk
USER desk
ENV DESK_ADDR=:8090 DESK_DATA=/data
VOLUME ["/data"]
EXPOSE 8090
ENTRYPOINT ["/usr/local/bin/desk"]
