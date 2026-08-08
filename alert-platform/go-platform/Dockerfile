FROM golang:1.25-alpine AS builder

WORKDIR /src/go-platform
COPY go-platform/go.mod go-platform/go.sum ./
RUN go mod download
COPY go-platform/ .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /gateway ./cmd/gateway
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /delivery-planner ./cmd/delivery-planner
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /pipeline-worker ./cmd/pipeline-worker
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /kb-index ./cmd/kb-index

FROM alpine:3.22
RUN addgroup -S app && adduser -S -G app app
COPY --from=builder /gateway /usr/local/bin/gateway
COPY --from=builder /delivery-planner /usr/local/bin/delivery-planner
COPY --from=builder /pipeline-worker /usr/local/bin/pipeline-worker
COPY --from=builder /kb-index /usr/local/bin/kb-index
COPY connectors/ /app/connectors/
COPY packages/rules/priority_matrix.yaml /app/priority_matrix.yaml
COPY packages/rules/runbooks.yaml /app/runbooks.yaml
COPY knowledge_base/ /app/knowledge_base/
USER app
EXPOSE 8080
ENTRYPOINT ["/usr/local/bin/gateway"]
