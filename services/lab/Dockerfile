FROM golang:1.26-alpine AS build
WORKDIR /src
ENV GOWORK=off
COPY go.* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -mod=readonly -trimpath -ldflags='-s -w' -o /out/service ./cmd/service
RUN CGO_ENABLED=0 go build -mod=readonly -trimpath -ldflags='-s -w' -o /out/worker ./cmd/worker

FROM alpine:3.23 AS worker
RUN apk add --no-cache ca-certificates docker-cli && addgroup -S -g 10001 qslate && adduser -S -D -H -u 10001 -G qslate qslate
WORKDIR /app
COPY --from=build /out/worker /app/worker
COPY config/lab.json /app/config/lab.json
USER 10001:10001
ENTRYPOINT ["/app/worker"]

# Default build is the API; the privileged worker is an explicit target.
FROM alpine:3.23 AS api
RUN apk add --no-cache ca-certificates && addgroup -S -g 10001 qslate && adduser -S -D -H -u 10001 -G qslate qslate
WORKDIR /app
COPY --from=build /out/service /app/service
COPY config/lab.json /app/config/lab.json
USER 10001:10001
EXPOSE 8080
ENV HTTP_ADDR=:8080
ENTRYPOINT ["/app/service"]
