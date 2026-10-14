FROM python:3.12-slim-bookworm

ENV PIP_DISABLE_PIP_VERSION_CHECK=1 \
    PIP_NO_CACHE_DIR=1 \
    PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    DBT_ENGINE_PROFILES_DIR=/opt/dbt \
    DBT_ENGINE_SEND_ANONYMOUS_USAGE_STATS=false

RUN apt-get update \
    && apt-get install -y --no-install-recommends git ca-certificates \
    && rm -rf /var/lib/apt/lists/*

COPY python.requirements.txt dbt.requirements.txt /tmp/
RUN python -m pip install --no-cache-dir \
        -r /tmp/python.requirements.txt -r /tmp/dbt.requirements.txt \
    && python -m pip check \
    && useradd --create-home --uid 10001 tool

COPY profiles.yml /opt/dbt/profiles.yml
RUN mkdir -p /artifacts /opt/dbt \
    && chown -R tool:tool /artifacts /opt/dbt

USER 10001:10001
WORKDIR /workspace
ENTRYPOINT ["python", "-m", "orchestration.pipeline"]
CMD ["--help"]
