FROM golang:1.22-bookworm AS build
WORKDIR /src
COPY go.mod ./
COPY *.go ./
RUN CGO_ENABLED=0 go build -o /egress-proxy .

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=build /egress-proxy /egress-proxy
USER nonroot
EXPOSE 3128
ENTRYPOINT ["/egress-proxy"]
