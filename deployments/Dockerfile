FROM golang:1.24-alpine AS build

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -o prreviewer ./cmd/prreviewer
RUN go build -o loadtest ./tools/loadtest.go

FROM alpine:3.20
WORKDIR /app
COPY --from=build /app/prreviewer /app/prreviewer
COPY --from=build /app/loadtest /app/loadtest
EXPOSE 8080
ENTRYPOINT ["/app/prreviewer"]
