FROM golang:1.27.1-alpine AS build
WORKDIR /src
ARG GOPROXY=https://proxy.golang.org,direct
ENV GOPROXY=$GOPROXY
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod --mount=type=cache,target=/root/.cache/go-build CGO_ENABLED=0 go build -o /out/api . && CGO_ENABLED=0 go build -o /out/migrate ./cmd/migrate
FROM alpine:3.23
RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=build /out/ /app/
COPY etc/ ./etc/
COPY db/migrations/ ./db/migrations/
USER 65532:65532
ENTRYPOINT ["/app/api"]
