FROM golang:1.26-alpine AS build
WORKDIR /src
ENV GOWORK=off
COPY go.* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -mod=readonly -trimpath -ldflags='-s -w' -o /out/service ./cmd/service

FROM alpine:3.23
RUN apk add --no-cache ca-certificates && addgroup -S -g 10001 qslate && adduser -S -D -H -u 10001 -G qslate qslate
WORKDIR /app
COPY --from=build /out/service /app/service
COPY config/layout.json /app/layout.json
ENV WORKSPACE_LAYOUT_FILE=/app/layout.json
USER 10001:10001
EXPOSE 8080
ENV HTTP_ADDR=:8080
ENTRYPOINT ["/app/service"]
