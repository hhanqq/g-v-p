# Мультистейдж-сборка фронтенда платформы (Этап 1, план —
# ~/.claude/plans/cheerful-mixing-pillow.md). Только admin-console
# собирается этим файлом — gateway/worker/delivery продолжают собираться
# обычным корневым Dockerfile, чтобы не тащить node в сервисы, которым
# он не нужен.

FROM node:20-alpine AS web-builder
WORKDIR /web
COPY web/package.json ./
# ARG, не ENV — тот же приём, что и с PIP_INDEX_URL в корневом Dockerfile:
# сеть сборочного хоста может резать конкретные файлы registry.npmjs.org
# (поймано вживую при локальной сборке), нужно уметь подставить зеркало
# только на время build, не в рантайме.
ARG NPM_REGISTRY=https://registry.npmjs.org
RUN npm install --registry "$NPM_REGISTRY"
COPY web/ ./
RUN npm run build

FROM python:3.11-slim
WORKDIR /app

RUN apt-get update && apt-get install -y --no-install-recommends libmagic1 \
    && rm -rf /var/lib/apt/lists/*

COPY requirements.txt .
ARG PIP_INDEX_URL=https://pypi.org/simple
RUN pip install --no-cache-dir --index-url "$PIP_INDEX_URL" -r requirements.txt

COPY connectors/ connectors/
COPY packages/ packages/
COPY services/ services/
COPY datagen/ datagen/
COPY scripts/ scripts/
COPY --from=web-builder /web/dist/ web/dist/

ENV PYTHONPATH=/app
