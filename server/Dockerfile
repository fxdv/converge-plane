# syntax=docker/dockerfile:1

# The Go toolchain must satisfy the version directive in go.mod and be a
# supported release (security fixes land only in the two newest). Keep in
# step with .github/workflows/ci.yml.
FROM golang:1.27-alpine AS build
WORKDIR /src
ARG VERSION=0.1.0-dev
ARG COMMIT=dev
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build \
    -ldflags "-s -w -X main.version=${VERSION} -X main.commit=${COMMIT}" \
    -o /out/converge ./cmd/converge

FROM alpine:3.24
RUN adduser -D -u 10001 converge
USER converge
COPY --from=build /out/converge /usr/local/bin/converge
EXPOSE 3001
ENV CONVERGE_HTTP_ADDR=:3001
ENTRYPOINT ["converge"]
