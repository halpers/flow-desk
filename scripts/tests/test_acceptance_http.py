"""Verify throttled logins recover without masking other acceptance failures."""

import json
import unittest
from unittest.mock import patch

import httpx

from scripts import acceptance_http


class LoginRecoveryTests(unittest.TestCase):
    def test_throttled_login_retries_same_credentials_and_returns_success(self):
        requests = []

        def respond(request):
            requests.append(json.loads(request.content))
            if len(requests) == 1:
                return httpx.Response(
                    429, json={"code": "RATE_LIMITED"}, headers={"Retry-After": "1.5"}
                )
            return httpx.Response(
                200, json={"data": {"access_token": "synthetic-token"}}
            )

        with (
            httpx.Client(
                base_url="https://example.invalid/api/v1",
                transport=httpx.MockTransport(respond),
            ) as client,
            patch.object(acceptance_http.time, "sleep") as pause,
        ):
            response = acceptance_http.login_with_backoff(
                client, "synthetic.account", "synthetic-password"
            )
        self.assertEqual(response.status_code, 200)
        self.assertEqual(len(requests), 2)
        self.assertEqual(requests[0], requests[1])
        pause.assert_called_once_with(1.5)

    def test_persistent_limit_stops_at_named_attempt_bound(self):
        with (
            httpx.Client(
                base_url="https://example.invalid/api/v1",
                transport=httpx.MockTransport(
                    lambda request: httpx.Response(429, json={"code": "RATE_LIMITED"})
                ),
            ) as client,
            patch.object(acceptance_http.time, "sleep") as pause,
        ):
            response = acceptance_http.login_with_backoff(
                client, "synthetic", "synthetic"
            )
        self.assertEqual(response.status_code, 429)
        self.assertEqual(pause.call_count, 5)

    def test_unauthorized_is_not_retried(self):
        with (
            httpx.Client(
                base_url="https://example.invalid/api/v1",
                transport=httpx.MockTransport(
                    lambda request: httpx.Response(401, json={"code": "AUTH_REQUIRED"})
                ),
            ) as client,
            patch.object(acceptance_http.time, "sleep") as pause,
        ):
            response = acceptance_http.login_with_backoff(
                client, "synthetic", "synthetic"
            )
        self.assertEqual(response.status_code, 401)
        pause.assert_not_called()

    def test_other_or_malformed_429_is_not_retried(self):
        for body in (b'{"code":"OTHER"}', b"not-json", b"[]"):
            with (
                self.subTest(body=body),
                httpx.Client(
                    base_url="https://example.invalid/api/v1",
                    transport=httpx.MockTransport(
                        lambda request, response_body=body: httpx.Response(
                            429, content=response_body
                        )
                    ),
                ) as client,
                patch.object(acceptance_http.time, "sleep") as pause,
            ):
                response = acceptance_http.login_with_backoff(
                    client, "synthetic", "synthetic"
                )
                self.assertEqual(response.status_code, 429)
                pause.assert_not_called()

    def test_delay_header_is_bounded_and_invalid_values_use_default(self):
        for header, expected in (
            ("NaN", 1.0),
            ("invalid", 1.0),
            ("100", 2.0),
            ("0", 0.5),
        ):
            calls = []

            def respond(request, attempts=calls, retry_after=header):
                attempts.append(request)
                return (
                    httpx.Response(
                        429,
                        json={"code": "RATE_LIMITED"},
                        headers={"Retry-After": retry_after},
                    )
                    if len(attempts) == 1
                    else httpx.Response(200, json={})
                )

            with (
                self.subTest(header=header),
                httpx.Client(
                    base_url="https://example.invalid/api/v1",
                    transport=httpx.MockTransport(respond),
                ) as client,
                patch.object(acceptance_http.time, "sleep") as pause,
            ):
                acceptance_http.login_with_backoff(client, "synthetic", "synthetic")
                pause.assert_called_once_with(expected)

    def test_network_failure_is_not_silently_retried(self):
        def timeout(request):
            raise httpx.ReadTimeout("synthetic timeout", request=request)

        with (
            httpx.Client(
                base_url="https://example.invalid/api/v1",
                transport=httpx.MockTransport(timeout),
            ) as client,
            patch.object(acceptance_http.time, "sleep") as pause,
        ):
            with self.assertRaises(httpx.ReadTimeout):
                acceptance_http.login_with_backoff(client, "synthetic", "synthetic")
            pause.assert_not_called()
