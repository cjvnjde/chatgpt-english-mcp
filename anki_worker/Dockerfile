# syntax=docker/dockerfile:1.27.0@sha256:bde3983e9c939224420ddaf6b784cc30e09b035a4dea01f581230c50809f372e

FROM python:3.14.7-slim-trixie@sha256:cad9a2c871761c413caa6fdd6441c783451e740a48aaeba60ae62a8b53525ef6

ENV PYTHONUNBUFFERED=1 \
    PYTHONDONTWRITEBYTECODE=1 \
    HOME=/data

WORKDIR /app
COPY anki_worker/requirements.txt /app/anki_worker/requirements.txt
RUN pip install --no-cache-dir --require-hashes --only-binary=:all: -r /app/anki_worker/requirements.txt \
    && mkdir -p /data \
    && chown 10001:10001 /data \
    && chmod 700 /data

COPY anki_worker/ /app/anki_worker/
USER 10001:10001
ENTRYPOINT ["python", "-m", "anki_worker"]
CMD ["run"]
