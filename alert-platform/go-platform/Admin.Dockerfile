FROM node:20-alpine AS web-builder
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.25-alpine AS go-builder
WORKDIR /src/go-platform
COPY go-platform/go.mod go-platform/go.sum ./
RUN go mod download
COPY go-platform/ ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /admin-api ./cmd/admin-api

FROM alpine:3.22
RUN addgroup -S app && adduser -S -G app app
COPY --from=go-builder /admin-api /usr/local/bin/admin-api
COPY --from=web-builder /web/dist /app/web
USER app
EXPOSE 8090
ENTRYPOINT ["/usr/local/bin/admin-api"]
