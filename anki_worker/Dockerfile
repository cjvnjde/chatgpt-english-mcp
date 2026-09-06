# syntax=docker/dockerfile:1

FROM python:3.14.7-slim-trixie

ENV PYTHONUNBUFFERED=1 \
    PYTHONDONTWRITEBYTECODE=1 \
    HOME=/data

WORKDIR /app
COPY anki_worker/requirements.txt /app/anki_worker/requirements.txt
RUN pip install --no-cache-dir -r /app/anki_worker/requirements.txt \
    && mkdir -p /data \
    && chown 10001:10001 /data \
    && chmod 700 /data

COPY anki_worker/ /app/anki_worker/
USER 10001:10001
ENTRYPOINT ["python", "-m", "anki_worker"]
CMD ["run"]
