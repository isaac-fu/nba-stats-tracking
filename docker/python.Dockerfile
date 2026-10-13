FROM python:3.12-slim-bookworm

ENV PIP_DISABLE_PIP_VERSION_CHECK=1 \
    PIP_NO_CACHE_DIR=1 \
    PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1

COPY python.requirements.txt /tmp/python.requirements.txt
RUN python -m pip install --no-cache-dir -r /tmp/python.requirements.txt \
    && python -m pip check \
    && useradd --create-home --uid 10001 tool

USER 10001:10001
WORKDIR /workspace
CMD ["python", "--version"]
