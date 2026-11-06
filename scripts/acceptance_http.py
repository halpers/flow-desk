"""Bounded recovery from shared-runner login throttling in acceptance scripts."""

import math
import time

import httpx

MAXIMUM_LOGIN_ATTEMPTS = 6
DEFAULT_LOGIN_RETRY_DELAY = 1.0
MINIMUM_LOGIN_RETRY_DELAY = 0.5
MAXIMUM_LOGIN_RETRY_DELAY = 2.0


def login_with_backoff(
    client: httpx.Client, account: str, password: str
) -> httpx.Response:
    """Retry only explicit login RATE_LIMITED responses; preserve other failures."""
    attempts = 0
    while True:
        attempts += 1
        response = client.post(
            "/auth/login", json={"account": account, "password": password}
        )
        if response.status_code != 429 or attempts >= MAXIMUM_LOGIN_ATTEMPTS:
            return response
        try:
            rate_limited = response.json().get("code") == "RATE_LIMITED"
        except (ValueError, AttributeError):
            rate_limited = False
        if not rate_limited:
            return response
        try:
            delay = float(
                response.headers.get("Retry-After", DEFAULT_LOGIN_RETRY_DELAY)
            )
        except (ValueError, TypeError):
            delay = DEFAULT_LOGIN_RETRY_DELAY
        if not math.isfinite(delay):
            delay = DEFAULT_LOGIN_RETRY_DELAY
        delay = min(MAXIMUM_LOGIN_RETRY_DELAY, max(MINIMUM_LOGIN_RETRY_DELAY, delay))
        response.close()
        time.sleep(delay)
