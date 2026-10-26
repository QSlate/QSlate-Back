FROM python:3.13-alpine
RUN addgroup -S -g 10001 qslate && adduser -S -D -H -u 10001 -G qslate qslate
WORKDIR /app
COPY server.py check.py config.json ./
USER 10001:10001
EXPOSE 8080
ENTRYPOINT ["python", "-I", "-B", "/app/server.py"]
