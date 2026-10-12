FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
ARG SERVICE_PATH
RUN test -n "$SERVICE_PATH" && CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /out/service ./$SERVICE_PATH

FROM alpine:3.23
RUN apk add --no-cache ca-certificates && addgroup -S -g 10001 qslate && adduser -S -D -H -u 10001 -G qslate qslate
WORKDIR /app
COPY --from=build /out/service /app/service
COPY gateway/config/routes.json /app/routes.json
USER 10001:10001
EXPOSE 8080
ENTRYPOINT ["/app/service"]
