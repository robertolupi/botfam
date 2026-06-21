# botfam binary image — entrypoint is the binary, so the compose `command:`
# (or `docker run ... <subcommand>`) selects what runs.
# Use a Go toolchain that satisfies go.mod's `go` directive (>=1.26).
FROM golang:1.26-alpine AS build
RUN apk add --no-cache git
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /botfam ./cmd/botfam

FROM alpine:3.22
COPY --from=build /botfam /botfam
ENTRYPOINT ["/botfam"]
