# syntax=docker/dockerfile:1
FROM golang:1.27.3-alpine

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -o /case-watch .

CMD ["/case-watch"]
