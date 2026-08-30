# syntax=docker/dockerfile:1

FROM golang:1.24-alpine AS build
WORKDIR /src
ARG VERSION=0.1.0-dev
ARG COMMIT=dev
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build \
    -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${COMMIT}" \
    -o /out/circle ./cmd/circle

FROM alpine:3.20
RUN adduser -D -u 10001 circle
USER circle
COPY --from=build /out/circle /usr/local/bin/circle
EXPOSE 3001
ENV CIRCLE_HTTP_ADDR=:3001
ENTRYPOINT ["circle"]
