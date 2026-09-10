# syntax=docker/dockerfile:1@sha256:ecfaec9ed6d810b56388c508f4121597bfbba70d41a6dfeee4d8cad5f295fc32

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
