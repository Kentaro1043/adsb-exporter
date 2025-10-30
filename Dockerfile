# Build stage
FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o exporter .

# Runtime stage
FROM alpine:3.22.1
COPY --from=build /src/exporter /usr/local/bin/exporter
EXPOSE 9100
ENTRYPOINT ["/usr/local/bin/exporter","-events","/app/events.yaml","-listen",":9100"]