# Asiatech SMS API — Backend Integration Guide

This document is a condensed implementation handoff for backend developers. It removes document history, screenshots, business prose, and repetitive examples, and keeps only the API contract plus implementation cautions and side effects.

## 1. Base URL

```text
https://smsapi.asiatech.ir
```

WebEngage uses a different host:

```text
https://smsapi-ext.asiatech.ir
```

## 2. Production prerequisites

- The production server IP must be permitted/whitelisted by Asiatech before the APIs are operational.
- Supported authentication methods:
  - Bearer token
  - API key
  - Basic Authentication — supported but explicitly not recommended by Asiatech for security reasons.
- Prefer one authentication strategy consistently in the backend.

## 3. Rate limits

| API group | Limit |
|---|---:|
| `GET DLR` | 10 RPS |
| Send | 100 RPS |
| OTP | 100 RPS |
| WebEngage | 200 RPS |
| Other methods, total | 100 RPS |

There is also an account-specific **MPS** limit: SMS message parts per second. Retrieve the assigned value from `UserInfo`.

When a rate limit is exceeded:

```text
HTTP 429
resultCode: 118
message: Rate Limit Exceeded / Exceeded Rate Limit
```

### Implementation requirement

The client should rate-limit locally and should not rely on HTTP 429 as flow control.

---

# 4. Request validation rules

Asiatech performs strict request validation.

- Invalid fields, incorrect types, malformed JSON, or invalid values can cause the **entire request** to be rejected with HTTP `400`.
- Optional fields that have no value must be **omitted**. Do not send optional fields as `null` or empty values.
- This is especially important for bulk/list requests: one malformed item may reject the complete request.

Recommended backend behavior:

1. Validate locally before calling Asiatech.
2. Remove undefined/null/empty optional properties before serialization.
3. Log the Asiatech `resultCode` separately from the HTTP status code.

---

# 5. Authentication

## 5.1 Bearer token

### Endpoint

```http
POST /connect/token
Content-Type: application/x-www-form-urlencoded
```

### Request

```text
username=YOUR_USERNAME&password=YOUR_PASSWORD&scope=ApiAccess
```

### Fields

| Field | Type | Description |
|---|---|---|
| `username` | string | Account username |
| `password` | string | Account password |
| `scope` | string | Method-specific scope |

### Response

```json
{
  "access_token": "...",
  "token_type": "Bearer",
  "expires_in": 300,
  "expires_at": "2026-08-11T10:06:28Z",
  "scope": "ApiAccess"
}
```

### Important token side effects

- `expires_at` is UTC/ISO-8601 and must be respected.
- Token lifetime may change; do not hard-code a fixed TTL.
- **Issuing a new token automatically invalidates previous tokens.**
- Scopes are method-specific. A token created for one scope must not be assumed to work for another endpoint.
- If no scope is supplied, Asiatech defaults to `ApiAccess`.

### Recommended token implementation

Use a process-wide/shared token cache per account + scope. Avoid multiple workers independently refreshing tokens, because a new token invalidates the previous one and can cause workers to invalidate each other's credentials.

A distributed lock around token refresh is strongly recommended when more than one application instance uses the same Asiatech account.

---

## 5.2 API key

The source documentation describes API-key authentication using an API key created in the Asiatech dashboard.

The document's RTL rendering makes the generic API-key header name ambiguous (`X-API-Key` vs visually reversed text). The WebEngage section explicitly uses:

```http
X-API-Key: YOUR_API_KEY
```

**Verify the exact header name with Asiatech before production use if using API-key authentication for the normal SMS endpoints.**

---

# 6. Scopes

| Feature | Scope |
|---|---|
| Standard send | `ApiAccess` |
| Bulk / P2P bulk | `BulkApiAccess` |
| Pattern/template send | `ApiPatternAccess` |
| Scheduled send | `MessageRequestAccess` |
| OTP | `OtpAccess` |
| MO/DLR Pull | `ApiAccess` or `BulkApiAccess` |

---

# 7. Standard SMS send

Recommended transport: `POST`.

## Endpoint

```http
POST /api/{apiVersion}/message/send
Content-Type: application/json
Authorization: Bearer {token}
```

Scope:

```text
ApiAccess
```

If the version is omitted, Asiatech defaults to API version 1.

## Request body

The endpoint accepts a list of messages:

```json
[
  {
    "SourceAddress": "989000XXXX",
    "DestinationAddress": "98912XXXXXXX",
    "MessageText": "Test message",
    "TargetUDH": ["referenceNumberType:16bit"],
    "udh": "campaign-123"
  }
]
```

### Fields

| Field | Required | Type | Notes |
|---|---:|---|---|
| `SourceAddress` | yes | string | Sender number |
| `DestinationAddress` | yes | string | Recipient number |
| `MessageText` | yes | string | Message body |
| `ValidityPeriod` | no | ISO-8601 datetime | Requires coordination with Asiatech; see cautions below |
| `TargetUDH` | no | string[] | Maximum one value; `referenceNumberType:8bit` or `referenceNumberType:16bit` |
| `udh` | no | string | User-defined metadata; not unique on Asiatech side |

### `TargetUDH` considerations

- Maximum one item.
- Allowed values:

```text
referenceNumberType:8bit
referenceNumberType:16bit
```

- For frequent messages from the same sender to the same recipient, Asiatech recommends 16-bit reference numbers to reduce multipart display issues.

### `ValidityPeriod`

According to the source document:

- must be at least 1 hour from the current time;
- must not exceed 4 hours from the current time;
- requires coordination with Asiatech technical support before use.

Use a valid ISO-8601 datetime. The sample in the Persian source is visibly malformed by RTL formatting, so do not copy it literally.

### `udh` side effect

`udh` is **not unique** in Asiatech. Duplicate values are allowed. Do not use it as the database primary key or as an idempotency guarantee.

---

# 8. Send API versions

Input format is the same. Output differs.

## Version 1

Returns message IDs only:

```json
{
  "message": "Successfully done.",
  "succeeded": true,
  "data": ["624ed1bbcd0efb1ef148e0a0"],
  "resultCode": 100
}
```

## Version 2

Returns message ID + MCC/MNC operator code.

```json
{
  "data": [
    {
      "key": "63f4a8772d1ae883cdd8ea76",
      "value": "43211"
    }
  ]
}
```

Common MCC/MNC values in the document:

| Code | Operator |
|---|---|
| `43211` | MCI |
| `43235` | MTN Irancell |
| `43220` | Rightel |
| `43208` | Shatel Mobile |
| `43210` | SamanTel |
| `43201` | UpTel / AzarTel |
| `43222` | ArianTel |
| `43221` | Fanap |
| `43214` | Kish Telecom |
| `43219` | Espadana |
| `43232` | Taliya |
| `432990` | LotusTel |

## Version 3

Returns message ID + number of SMS parts.

```json
{
  "data": [
    {
      "key": "63f4a8772d1ae883cdd8ea76",
      "value": "3"
    }
  ]
}
```

## Version 4

Returns the richest response and is the most useful for backend persistence:

```json
{
  "data": [
    {
      "id": "6753fea42e0d853017a20ba8",
      "part": "3",
      "upstreamGateway": "Rightel"
    }
  ],
  "resultCode": 100
}
```

Gateway aliases noted in the source:

- `MCI`, `SMCI`, `2SMCI`, `3SMCI` -> MCI
- `MTN`, `SMTN` -> Irancell
- `ApTel` -> ApTel
- `Rightel` -> Rightel

### Recommended choice

Use **API v4** unless compatibility requires another version. It gives the backend the message ID, part count, and upstream gateway in one response.

---

# 9. Long numeric IDs

Normal successful message IDs are UID strings.

If a numeric/long ID is required:

```text
?returnLongId=true
```

Example:

```http
POST /api/message/send?returnLongId=true
```

### Important consistency rule

If a message was sent with `returnLongId=true`, DLR retrieval must also use `returnLongId=true`.

---

# 10. Bulk SMS — one message to many recipients

## Endpoint

```http
POST /api/{apiVersion}/message/bulk
```

Scope:

```text
BulkApiAccess
```

## Request

```json
{
  "SourceAddress": "989000XXXX",
  "DestinationAddress": [
    "98912XXXXXXX",
    "98913XXXXXXX"
  ],
  "MessageText": "Same message for all recipients",
  "TargetUDH": ["referenceNumberType:16bit"],
  "udh": "campaign-123"
}
```

Fields and response version semantics are otherwise the same as standard send.

Use:

```text
?returnLongId=true
```

if numeric IDs are needed.

---

# 11. P2P bulk — different message per recipient

Use when each recipient can have a different sender/message.

## Endpoint

The source prints the path as:

```text
/api/{apiVersion}/message/ P2PBulk
```

The whitespace appears to be a formatting artifact. The expected path is likely:

```http
POST /api/{apiVersion}/message/P2PBulk
```

**Verify the exact production path with Asiatech before implementation.**

Scope:

```text
BulkApiAccess
```

## Request

```json
[
  {
    "SourceAddress": "9890001234",
    "DestinationAddress": "989123456789",
    "MessageText": "Message 1"
  },
  {
    "SourceAddress": "9890001235",
    "DestinationAddress": "989113456789",
    "MessageText": "Message 2"
  }
]
```

---

# 12. Pattern/template SMS

A pattern must first be created in the Asiatech dashboard and approved.

Parameters are positional and indexes begin at `0`.

## Single pattern request

```http
POST /api/PatternMessage/send
```

## Multiple pattern requests

```http
POST /api/PatternMessage/sendMultiple
```

Scope:

```text
ApiPatternAccess
```

## Request schema

```json
{
  "destinations": [
    "989XXXXXXXXX",
    "989XXXXXXXXX"
  ],
  "parameters": [
    "value0",
    "value1"
  ],
  "patternId": "638698763364455987"
}
```

### Important behavior

For each number in `destinations`, one SMS is created/sent.

`parameters` must exactly match the positional placeholders configured in the approved pattern.

Numeric IDs can be requested with:

```text
/api/patternMessage/send?returnLongId=true
```

---

# 13. Scheduled bulk send

## Endpoint

```http
POST /api/1/Schedule/SendOnce
```

Scope:

```text
MessageRequestAccess
```

## Request

```json
{
  "sourceAddress": "989000XXXX",
  "messageText": "Scheduled Text",
  "dueDate": "2026-11-26T18:01:00",
  "destinationAddressList": [
    "989XXXXXXXXX"
  ],
  "campaignName": "fbadb698-d00f-42d1-90d6-65f19676eb83"
}
```

### Fields

| Field | Type |
|---|---|
| `sourceAddress` | string |
| `destinationAddressList` | string[] |
| `messageText` | string |
| `dueDate` | datetime string |
| `campaignName` | string |

### Limits / side effects

- Maximum **100 recipients per request**.
- The response `data` is the scheduled-message request ID.
- Scheduled messages can be viewed and deleted from the panel.
- **Scheduled messages cannot be edited after creation.** To change one, delete it and create a new request.

---

# 14. OTP send

## Endpoint

```http
POST /api/1/Otp/SendOtp
```

Scope:

```text
OtpAccess
```

Only API version `1` is documented.

Maximum content size: **4 SMS parts**.

## Request

```json
{
  "sourceAddress": "989000XXXX",
  "destinationAddress": "989XXXXXXXXX",
  "messageText": "Verification code: 1255"
}
```

Invalid OTP content/length can return:

```text
HTTP 400
resultCode 2412
Request rejected: Invalid content or length.
```

---

# 15. SMS segmentation / part calculation

Multipart billing and throughput are based on SMS parts, not logical messages.

| Encoding / reference type | Single part | Multipart chars per part |
|---|---:|---:|
| Persian/Unicode + 16-bit ref | 70 | 66 |
| GSM-7 + 16-bit ref | 160 | 152 |
| Persian/Unicode + 8-bit ref | 70 | 67 |
| GSM-7 + 8-bit ref | 160 | 153 |

GSM-7 extension characters such as `€ | ] ~ [ \\ } { ^` consume two GSM characters.

If the message contains even one character outside the documented GSM-7 character set, Asiatech treats the message as Persian/Unicode for part calculation.

### Backend implication

Do not estimate billing or MPS from `MessageText.length`. Use GSM-7-aware segmentation logic, or persist the returned `part` value from API v4.

---

# 16. Incoming SMS (MO)

Asiatech supports receiving MO by webhook (`GET`/`POST`), SMPP, or Pull API.

## Pull unread MO

```http
GET /api/message/getmo
```

Scope:

```text
ApiAccess
```

or

```text
BulkApiAccess
```

Returns up to **1000 unread messages per call**.

Optional query parameter:

```text
?returnId=true
```

Typical item:

```json
{
  "sourceAddress": "9891XXXXXXXX",
  "destinationAddress": "989000XXXX",
  "messageText": "Incoming message",
  "receiveDateTime": "2022-04-04T11:23:11.211Z"
}
```

When no unread item exists:

```json
{
  "message": "Item not found!",
  "succeeded": false,
  "resultCode": 109
}
```

## MO by date

```http
GET /api/message/getmobydate
```

Parameters:

```text
startDateTime=2024-09-01T09:53:20
endDateTime=2024-09-09T09:53:20
```

Optional:

```text
returnId=true
```

### Important mode interaction

If MO delivery is configured via SMPP or webhook (`POST/GET` to your endpoint), `getmo` is no longer available for that account; `getmobydate` remains available.

---

# 17. Delivery reports (DLR)

Asiatech supports DLR via webhook, SMPP, or Pull API.

## Webhook payload

A POST webhook contains fields similar to:

```json
[
  {
    "Status": "Delivered",
    "PartNumber": "1",
    "MessageId": "68a18fe145422a822f28d7d2",
    "DateTime": "8/17/2025 11:46:45 AM",
    "Mobile": "989XXXXXXXXX",
    "UserName": "test",
    "FullDelivery": true,
    "SenderId": "989XXXXXXX",
    "UDH": null,
    "ErrorCode": "000"
  }
]
```

## Pull DLR

```http
POST /api/message/getdlr
```

Scope:

```text
ApiAccess
```

or

```text
BulkApiAccess
```

The request body is a list of message IDs:

```json
[
  "624ed1bbcd0efb1ef148e0a0",
  "624ed1bbcd0efb1ef148e0a1"
]
```

Asiatech documents a maximum of **1000 message statuses per call**.

### Response structure

```json
{
  "message": "Successfully done.",
  "succeeded": true,
  "data": [
    {
      "id": "624ed1bbcd0efb1ef148e0a0",
      "partStatus": [
        {
          "item1": 1,
          "item2": 34,
          "item3": "2022-04-07T11:57:50.044Z"
        }
      ],
      "deliveryStatus": 34
    }
  ],
  "resultCode": 100
}
```

`partStatus`:

| Field | Meaning |
|---|---|
| `item1` | SMS part sequence number |
| `item2` | Delivery status code |
| `item3` | Status timestamp received from upstream operator |

`deliveryStatus` is the overall logical-message status.

### Optional DLR response extensions

If send used long IDs:

```text
?returnLongId=true
```

Return the original `udh` value:

```text
?returnUDH=true
```

Return Asiatech creation/processing/upstream-send timestamps:

```text
?returnSentDate=true
```

---

# 18. Critical multipart DLR behavior

For multipart SMS, Asiatech may modify the **overall** status after part-level statuses arrive.

If:

- at least one part is `Delivered`, and
- remaining parts are `Undeliverable` and/or `Expired`, and
- 20 minutes have passed since the last delivery-status event,

Asiatech changes the overall logical-message status to:

```text
Enroute (33)
```

### Backend consequence

Do **not** treat the first DLR response as immutable.

For multipart messages with mixed part statuses, poll again at least 20 minutes later before considering the logical-message status final.

Store both:

1. per-part statuses; and
2. overall `deliveryStatus`.

Do not derive the overall status solely from the first observed part status.

---

# 19. Delivery status codes

| Code | Status | Meaning |
|---:|---|---|
| 1 | `Delivered` | Delivered to handset |
| 2 | `UnDelivered` | Not delivered to handset |
| 3 | `Accepted` | Accepted by operator |
| 4 | `ReceivedByUpstream` | Received by upstream provider |
| 5 | `Rejected` | Rejected by provider |
| 6 | `NotReceiveByServer` | Not received by server |
| 7 | `ErrorInSending` | Send error |
| 8 | `WaitingForSend` | Waiting to send |
| 9 | `Sent` | Sent |
| 10 | `NotSent` | Not sent |
| 11 | `Expired` | Expired |
| 12 | `IsSending` | Sending |
| 13 | `IsCanceled` | Canceled |
| 14 | `BlackList` | Blacklisted |
| 15 | `SmsIsFilter` | SMS content filtered |
| 16 | `Deleted` | Deleted |
| 17 | `WaitingForConfirmation` | Waiting for approval |
| 18 | `NotEnoughBalance` | Insufficient balance |
| 19 | `IsPreparing` | Preparing |
| 20 | `IsPreparedForSending` | Prepared for sending |
| 21 | `AccessDenied` | Access denied |
| 22 | `TextIsEmpty` | Empty text |
| 23 | `InvalidInputFormat` | Invalid input format |
| 24 | `InvalidUserOrPassword` | Invalid credentials |
| 25 | `InvalidUsedMethod` | Invalid method |
| 26 | `InvalidSender` | Invalid sender |
| 27 | `InvalidMobile` | Invalid mobile number |
| 28 | `InvalidReception` | No recipient |
| 29 | `Stored` | Stored |
| 30 | `BlackListTable` | Number exists in blacklist table |
| 31 | `GetDeliveryStatus` | Delivery-status lookup requested |
| 32 | `Unknown` | Unknown |
| 33 | `Enroute` | In route / final state unresolved |
| 34 | `Undeliverable` | Undeliverable |
| 35 | `MessageQueueFull` | Queue capacity full |
| 36 | `UnreachableNetwork` | Network unreachable |

---

# 20. Status finalization considerations

The source document distinguishes statuses that may remain non-final for a period.

Operationally important points:

- `Delivered` may require approximately **20 minutes** before the overall status is considered final for multipart handling.
- `Sent` may remain unresolved for up to approximately **7 hours**.
- `Undeliverable` may remain unresolved for up to approximately **7 hours**.
- Several other statuses are documented as immediately final.

### Backend recommendation

Maintain a `finalized_at` / `is_final` concept in your local model rather than assuming every DLR code is terminal.

For non-final statuses, schedule rechecks rather than overwriting and forgetting the previous state.

---

# 21. Chargeback / billing side effects

Billing is per SMS part.

The source document states the following chargeback behavior:

| Status | MCI | Irancell | Other operators |
|---|---|---|---|
| Delivered | charged | charged | charged |
| UnDelivered | chargeback | chargeback | charged |
| Accepted | charged | charged | charged |
| Rejected | chargeback | chargeback | charged |
| ErrorInSending | chargeback | chargeback | chargeback |
| Sent | charged | charged | charged |
| NotSent | chargeback | chargeback | chargeback |
| Expired | chargeback | chargeback | charged |
| Deleted | chargeback | chargeback | charged |
| Unknown | chargeback | chargeback | charged |
| Enroute | chargeback | chargeback | charged |
| Undeliverable | chargeback | chargeback | charged |
| UnreachableNetwork | chargeback | chargeback | charged |

For multipart messages whose overall status becomes `Enroute` because some parts were delivered and others were `Undeliverable`/`Expired`, the fee for the failed parts is returned as chargeback.

### Backend consequence

If local cost reporting is required, persist **part-level status and upstream operator**, not only logical-message status.

Do not mark cost as final at send time.

---

# 22. User information

Useful for health/config synchronization.

```http
GET /api/user/userinfo
```

Typical response:

```json
{
  "message": "Successfully done.",
  "succeeded": true,
  "data": {
    "userName": "username",
    "firstName": "name",
    "lastName": "family",
    "credit": 5000.0,
    "userPaymentType": "PrePaid",
    "mps": 1000,
    "senderIds": [
      "989000xxxx",
      "9890001xxx"
    ]
  },
  "resultCode": 100
}
```

Recommended uses:

- validate allowed sender numbers;
- fetch account credit;
- read MPS dynamically;
- operational diagnostics.

---

# 23. Connectivity / diagnostics

## Check client IP

```http
GET /api/Tools/CheckIp
```

Returns the client IP seen by Asiatech.

Useful when debugging IP whitelist issues or NAT/proxy changes.

## Health check

```http
GET /api/Tools/Ping
```

Expected successful response:

```text
PONG
```

---

# 24. Common API result codes

Do not check only the HTTP status. Parse `resultCode` and `succeeded` as well.

| HTTP | Result code | Meaning |
|---:|---:|---|
| 200 | 100 | Success |
| 500 | 101 | Database error |
| 500 | 102 | Repository error |
| 400 | 103 | Request/model error |
| 504 | 104 | API connection failure |
| 503 | 105 | Service unavailable |
| 404 | 107 | User not found |
| 409 | 108 | Duplicate item |
| 200 | 109 | Item/ID not currently available |
| 400 | 116 | Invalid request header |
| 429 | 118 | Rate limit exceeded |
| 403 | 119 | User tariff missing |
| 400 | 126 | Invalid source/sender address |
| 400 | 131 | Invalid date/time |
| 402 | 135 | Insufficient credit |
| 400 | 136 | Invalid message ID |
| 403 | 138 | Scope mismatch |
| 403 | 2403 | Endpoint not accessible |
| 403 | 2404 | Non-bulk endpoint used for bulk/high-rate traffic |
| 403 | 2405 | IP blocked |
| 401 | 2406 | Unauthorized / invalid credentials/API key |
| 405 | 2407 | Method not allowed |
| 413 | 2408 | Request body too large |
| 403 | 2409 | User-Agent blocked |
| 401 | 2410 | Invalid token |
| 401 | 2411 | Expired token |
| 400 | 2412 | Invalid OTP content or length |

---

# 25. Send-level error codes

A send operation can return HTTP `200` + top-level `resultCode: 100` while the send itself carries a send-level error. Therefore treat transport/API success and message acceptance as separate layers.

Important send error codes documented by Asiatech:

| Error code | Meaning |
|---:|---|
| `0` | Send error |
| `-1` | Insufficient credit |
| `-2` | Server error |
| `-3` | Account disabled |
| `-4` | Account expired |
| `-5` | Invalid username/password |
| `-6` | Authentication failure |
| `-7` | Server busy |
| `-8` | Recipient blacklisted |
| `-9` | Daily send limit exceeded |
| `-10` | Throughput/rate limit exceeded |
| `-11` | Invalid sender number |
| `-12` | Invalid recipient number |
| `-13` | Invalid destination network |
| `-14` | Network unreachable |
| `-15` | Sender number disabled |
| `-16` | Invalid sender-number format |
| `-17` | No tariff found |
| `-18` | Invalid sender IP |
| `-19` | Invalid pattern |
| `-20` | Sender number expired |
| `-21` | Message contains a link |
| `-22` | Invalid port |
| `-23` | Message too long |
| `-24` | Filtered word |
| `-25` | Invalid reference-number type |
| `-26` | Invalid UDH/TargetUDH |
| `-27` | Monthly send limit exceeded |
| `-28` | Data coding not allowed |
| `-29` | No route found |
| `-30` | Message contains script |
| `-31` | Required setting not found |
| `-32` | Content filtered |
| `-33` | Invalid character |
| `-34` | SMS wallet has insufficient credit |
| `-35` | Campaign name already exists |
| `-36` | Approver not found |
| `-37` | Scheduled due date is in the past |
| `-38` | Invalid message-request ID |
| `-39` | Message request is not waiting for approval |
| `-40` | Expiration date earlier than due date |
| `-41` | Scheduled-message limit exceeded |
| `-42` | Duplicate/excessive repeated request |
| `-43` | Cache server error |

---

# 26. WebEngage integration

This is a special integration rather than the normal backend send flow.

```http
POST https://smsapi-ext.asiatech.ir/api/webengage/SendSMS
X-API-Key: ...
Content-Type: application/json
```

Scope shown in the document:

```text
BulkApiAccess
```

Additional prerequisites:

- WebEngage source IPs must be supplied to Asiatech for IP permission.
- To forward delivery status back to WebEngage, the WebEngage token must be supplied to Asiatech.
- **Side effect:** once WebEngage delivery-status forwarding is enabled, Asiatech does not simultaneously support Relay-DLR callbacks for the account's other messages. Other messages can still be queried using `GetDLR`.

If WebEngage is not part of the current backend task, omit this integration entirely.

---

# 27. Suggested backend data model

At minimum persist:

```text
provider_message_id
provider_long_id           nullable
provider
source_address
destination_address
message_text or message_hash
udh                        nullable
api_version
part_count                 nullable
upstream_gateway           nullable
send_result_code
send_error_code            nullable
delivery_status_code       nullable
delivery_status_name       nullable
delivery_status_at         nullable
is_final                   boolean
created_at
updated_at
```

For multipart messages, use a child table such as:

```text
sms_delivery_parts
------------------
provider_message_id
part_number
status_code
status_name
status_at
```

If billing reconciliation matters, also persist charge/chargeback events separately rather than deriving historical billing from the latest DLR alone.

---

# 28. Recommended implementation rules

1. Prefer `POST` APIs; do not use the documented GET-send method unless required for legacy compatibility.
2. Prefer API v4 for standard/bulk sends because it returns `id`, `part`, and `upstreamGateway`.
3. Centralize authentication/token refresh.
4. Use a distributed token-refresh lock when the same credentials are used by multiple workers/instances.
5. Apply local RPS + MPS throttling.
6. Strip empty optional properties before serialization.
7. Persist Asiatech message IDs immediately after successful send.
8. Do not treat HTTP 200 as sufficient proof that the SMS was accepted/sent.
9. Keep send state and delivery state separate.
10. Persist part-level DLRs for multipart messages.
11. Re-query non-final/mixed multipart statuses after the documented wait window.
12. Treat billing as eventually consistent because chargebacks depend on final part/operator statuses.
13. Use `CheckIp` when diagnosing whitelist/network issues.
14. Never log credentials, API keys, access tokens, or full OTP values.
15. Add retry only for transient failures (`5xx`, network timeout, server busy). Avoid blind retry of ambiguous send requests because duplicate SMS can be generated.

---

# 29. Ambiguities to confirm with Asiatech before production

The source PDF contains several RTL/layout artifacts. Confirm these items directly with Asiatech instead of guessing:

- Exact API-key header for normal APIs (`X-API-Key` is explicit for WebEngage; the general section is visually reversed).
- Exact P2P bulk endpoint spelling/path.
- Exact supported `ValidityPeriod` datetime format; use ISO-8601 semantically, but do not copy the source example literally.
- Maximum number of items in standard `send` and `bulk` bodies. The document clearly gives `100` for scheduled recipients and `1000` for MO/DLR pull, but does not clearly state a general standard-send/bulk batch maximum in the extracted text.
- Whether query parameters such as `returnLongId`, `returnUDH`, and `returnSentDate` may be combined on the same request; the document documents them independently.

---

# 30. Minimal implementation flow

```text
startup
  -> CheckIp / Ping (optional diagnostics)
  -> UserInfo (load sender IDs + MPS)

send request
  -> validate locally
  -> calculate / estimate parts
  -> acquire method-specific bearer token
  -> apply RPS + MPS limiter
  -> POST send/bulk/OTP/etc.
  -> validate HTTP status + succeeded + resultCode + send-level result
  -> persist provider message ID(s), part count, gateway

DLR processing
  -> webhook OR periodic GetDLR
  -> persist partStatus + deliveryStatus
  -> mark terminal only according to finalization rules
  -> recheck mixed multipart / non-final statuses
  -> apply billing/chargeback reconciliation if needed
```

