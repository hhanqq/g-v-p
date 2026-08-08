FROM python:3.11-slim

WORKDIR /app

RUN apt-get update && apt-get install -y --no-install-recommends libmagic1 \
    && rm -rf /var/lib/apt/lists/*

COPY requirements.txt .
# ARG, не ENV — индекс нужен только во время сборки образа (сеть сборочного
# хоста может блокировать pypi.org по SNI, см. память проекта), рантайму
# контейнера это не касается и по умолчанию остаётся официальный PyPI.
ARG PIP_INDEX_URL=https://pypi.org/simple
RUN pip install --no-cache-dir --index-url "$PIP_INDEX_URL" -r requirements.txt

COPY connectors/ connectors/
COPY packages/ packages/
COPY services/ services/
COPY datagen/ datagen/
COPY scripts/ scripts/
COPY database/ database/
COPY contracts/ contracts/

ENV PYTHONPATH=/app
