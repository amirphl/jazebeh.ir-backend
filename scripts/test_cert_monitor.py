import unittest
from unittest import mock

import cert_monitor


class CertMonitorSMSTests(unittest.TestCase):
    def test_mock_alert_never_contacts_provider(self):
        config = {"EXTERNAL_SERVICES_MODE": "mock"}
        cert_monitor._validate_sms_config(config)
        with mock.patch("cert_monitor.requests.post") as post:
            cert_monitor.send_sms("989121234567", "alert", config, 5)
        post.assert_not_called()

    def test_payamsms_uses_oauth_and_payamsms_send_endpoint(self):
        token_response = mock.MagicMock()
        token_response.json.return_value = {"access_token": "access-token"}
        send_response = mock.MagicMock()
        send_response.json.return_value = [{"errorCode": None}]
        config = {
            "SMS_PROVIDER_DOMAIN": "payamsms",
            "SMS_SOURCE_NUMBER": "2000000020",
            "PAYAM_SMS_SYSTEM_NAME": "system",
            "PAYAM_SMS_USERNAME": "user",
            "PAYAM_SMS_PASSWORD": "secret",
            "PAYAM_SMS_ROOT_ACCESS_TOKEN": "root-token",
        }

        with mock.patch(
            "cert_monitor.requests.post",
            side_effect=[token_response, send_response],
        ) as post:
            cert_monitor.send_sms("989121234567", "certificate alert", config, 5)

        token_call, send_call = post.call_args_list
        self.assertEqual(token_call.args[0], cert_monitor.PAYAM_SMS_TOKEN_URL)
        self.assertEqual(token_call.kwargs["data"]["password"], "secret")
        self.assertNotIn("secret", token_call.args[0])
        self.assertEqual(
            token_call.kwargs["headers"]["Authorization"], "Basic root-token"
        )
        self.assertEqual(send_call.args[0], cert_monitor.PAYAM_SMS_SEND_URL)
        self.assertEqual(
            send_call.kwargs["headers"]["Authorization"], "Bearer access-token"
        )
        self.assertEqual(
            send_call.kwargs["json"]["smsItems"][0]["recipient"],
            "989121234567",
        )

    def test_payamsms_validation_does_not_require_generic_api_key(self):
        cert_monitor._validate_sms_config(
            {
                "SMS_PROVIDER_DOMAIN": "payamsms",
                "SMS_SOURCE_NUMBER": "2000000020",
                "PAYAM_SMS_SYSTEM_NAME": "system",
                "PAYAM_SMS_USERNAME": "user",
                "PAYAM_SMS_PASSWORD": "secret",
            }
        )

    def test_generic_provider_still_uses_api_key_endpoint(self):
        response = mock.MagicMock()
        response.json.return_value = [{"statusCode": 200, "status": "ACCEPTED"}]
        config = {
            "SMS_PROVIDER_DOMAIN": "sms.example.com",
            "SMS_API_KEY": "api-key",
            "SMS_SOURCE_NUMBER": "2000",
        }

        with mock.patch("cert_monitor.requests.post", return_value=response) as post:
            cert_monitor.send_sms("989121234567", "certificate alert", config, 5)

        self.assertEqual(
            post.call_args.args[0], "https://sms.example.com/api/v3.0.1/send"
        )
        self.assertEqual(post.call_args.kwargs["headers"]["x-api-key"], "api-key")


if __name__ == "__main__":
    unittest.main()
