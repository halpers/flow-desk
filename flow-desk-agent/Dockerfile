FROM python:3.12-slim
ARG PIP_INDEX_URL=https://pypi.org/simple
WORKDIR /app
COPY requirements.lock ./
RUN pip install --timeout 120 --retries 5 --no-cache-dir -r requirements.lock
COPY src/ ./src/
ENV PYTHONPATH=/app/src
USER 65532:65532
CMD ["uvicorn", "flowdesk_agent.api.main:app", "--host", "0.0.0.0", "--port", "8000"]
