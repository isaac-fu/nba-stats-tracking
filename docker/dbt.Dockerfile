FROM python:3.12-slim-bookworm

ENV PIP_DISABLE_PIP_VERSION_CHECK=1 \
    PIP_NO_CACHE_DIR=1 \
    PYTHONDONTWRITEBYTECODE=1 \
    PYTHONUNBUFFERED=1 \
    DBT_ENGINE_PROFILES_DIR=/opt/dbt \
    DBT_ENGINE_SEND_ANONYMOUS_USAGE_STATS=false \
    DBT_ENGINE_LOG_PATH=/tmp/dbt-logs \
    DBT_ENGINE_TARGET_PATH=/tmp/dbt-target

RUN apt-get update \
    && apt-get install -y --no-install-recommends git ca-certificates \
    && rm -rf /var/lib/apt/lists/*

COPY dbt.requirements.txt /tmp/dbt.requirements.txt
RUN python -m pip install --no-cache-dir -r /tmp/dbt.requirements.txt \
    && python -m pip check \
    && useradd --create-home --uid 10001 tool

COPY profiles.yml dbt_project.yml /opt/dbt/
RUN mkdir -p /opt/dbt/logs /opt/dbt/target \
    && chown -R tool:tool /opt/dbt
USER 10001:10001
WORKDIR /opt/dbt
ENTRYPOINT ["dbt"]
CMD ["--version"]
