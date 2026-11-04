FROM python:3.12-slim@sha256:b18b261e1b4974b4f321451ad5251ded85abb22c2b8b262da527dda1d031ab30
ARG PIP_INDEX_URL=https://pypi.org/simple
WORKDIR /app
COPY system-packages.lock ./
RUN apt-get update \
    && sed '/^#/d; /^$/d' system-packages.lock | xargs apt-get install -y --no-install-recommends \
    && rm -rf /var/lib/apt/lists/*
COPY requirements.lock ./
RUN pip install --timeout 120 --retries 5 --no-cache-dir -r requirements.lock
COPY src/ ./src/
ENV PYTHONPATH=/app/src
USER 65532:65532
CMD ["uvicorn", "flowdesk_agent.api.main:app", "--host", "0.0.0.0", "--port", "8000"]
