FROM golang:1.25-alpine AS builder

WORKDIR /src/go-platform
COPY go-platform/go.mod go-platform/go.sum ./
RUN go mod download
COPY go-platform/ .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /gateway ./cmd/gateway
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /delivery-planner ./cmd/delivery-planner
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /pipeline-worker ./cmd/pipeline-worker
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /kb-index ./cmd/kb-index
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /clickhouse-migrate ./cmd/clickhouse-migrate
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /changelog-worker ./cmd/changelog-worker
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /delivery-email ./cmd/delivery-email

FROM alpine:3.22
RUN addgroup -S app && adduser -S -G app app
COPY --from=builder /gateway /usr/local/bin/gateway
COPY --from=builder /delivery-planner /usr/local/bin/delivery-planner
COPY --from=builder /pipeline-worker /usr/local/bin/pipeline-worker
COPY --from=builder /kb-index /usr/local/bin/kb-index
COPY --from=builder /clickhouse-migrate /usr/local/bin/clickhouse-migrate
COPY --from=builder /changelog-worker /usr/local/bin/changelog-worker
COPY --from=builder /delivery-email /usr/local/bin/delivery-email
COPY connectors/ /app/connectors/
COPY packages/rules/priority_matrix.yaml /app/priority_matrix.yaml
COPY packages/rules/runbooks.yaml /app/runbooks.yaml
COPY knowledge_base/ /app/knowledge_base/
COPY database/clickhouse_migrations/ /app/clickhouse_migrations/
USER app
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/gateway"]
