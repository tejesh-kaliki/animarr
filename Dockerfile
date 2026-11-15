FROM golang:1.23-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/animarr ./cmd/animarr

FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -g 1000 animarr \
    && adduser -D -u 1000 -G animarr animarr
COPY --from=build /out/animarr /usr/local/bin/animarr
USER animarr
EXPOSE 8990
ENV DATA_PATH=/config \
    LIBRARY_DIR=/data/media/anime \
    DOWNLOADS_DIR=/data/downloads \
    LISTEN_ADDR=:8990 \
    TZ=Asia/Kolkata
HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
    CMD ["animarr", "health"]
ENTRYPOINT ["animarr"]
