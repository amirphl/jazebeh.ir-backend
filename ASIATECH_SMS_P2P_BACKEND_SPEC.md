# Asiatech SMS API — Backend Implementation Spec

**Scope:** Only the API material required to authenticate, send bulk P2P SMS, repeatedly query delivery status, inspect account credit/MPS, health-check the service, interpret all API/send/delivery codes, determine when delivery states are final, and account for operator-specific chargebacks.

**Source:** Asiatech SMS Hub API documentation, revision dated 1405/05/25.

> This document intentionally excludes unrelated API methods and administrative material.

---

## 1. Implementation Goal

The backend must be able to:

1. Authenticate reliably.
2. Respect both request-rate limits (RPS) and message-part throughput limits (MPS).
3. Send **bulk peer-to-peer (P2P)** messages, where each recipient can have its own sender/message body.
4. Persist every returned Asiatech message ID.
5. Query DLR/delivery status for those IDs repeatedly over time.
6. Store both per-part status and overall message status.
7. Re-query non-final / time-dependent statuses until they are final according to Asiatech's finalization rules.
8. Correctly interpret API-level errors, send-level error codes, and delivery-status codes.
9. Account for operator-specific chargebacks per SMS part.

### Recommended API version for this workflow

Use **API v4** for P2P bulk sending when available:

```text
POST https://smsapi.asiatech.ir/api/4/message/P2PBulk
```

The general versioning documentation says v1-v4 have the same inputs but different response payloads. The bulk v4 response includes:

- `id` — message ID used for DLR lookup
- `part` — number of SMS parts
- `upstreamGateway` — upstream/operator route

**Source inconsistency:** the P2P section labels its shown response as “version 1”, but the response shape contains `id`, `part`, and `upstreamGateway`, which the earlier bulk section defines as the v4 response shape. Therefore, for a deterministic backend implementation, explicitly request `/api/4/...` rather than relying on the unversioned/default behavior.

---

# 2. Authentication

Asiatech supports three authentication methods:

1. Basic Authentication
2. Bearer Token
3. API Key

For production use, Asiatech also requires the client's operational IP address to be permitted/whitelisted by Asiatech.

## 2.1 Basic Authentication

Asiatech explicitly states that Basic Authentication is **not recommended because of its security weaknesses**.

Construct:

```text
base64(username + ":" + password)
```

Then send:

```http
Authorization: Basic <base64-value>
```

Example conceptual input:

```text
AsiatechUser:AsiatechPassword
```

## 2.2 Bearer Token

### Token endpoint

```http
POST https://smsapi.asiatech.ir/connect/token
Content-Type: application/x-www-form-urlencoded
```

### Parameters

| Parameter | Type | Description |
|---|---|---|
| `scope` | string | Method-specific scope |
| `username` | string | Account username |
| `password` | string | Account password |

Example:

```text
username=YOUR_USERNAME&password=YOUR_PASSWORD&scope=BulkApiAccess
```

### Token response

```json
{
  "access_token": "Your token",
  "token_type": "Bearer",
  "expires_in": 300,
  "expires_at": "2026-08-11T10:06:28Z",
  "scope": "BulkApiAccess"
}
```

| Field | Type |
|---|---|
| `access_token` | string |
| `token_type` | `Bearer` |
| `expires_in` | integer, seconds |
| `expires_at` | ISO-8601 UTC datetime string |
| `scope` | string |

### Critical token behavior

- Tokens are scope-specific.
- If no scope is supplied, the system defaults to `ApiAccess`.
- Token expiration must be read from the returned expiration fields; do not hard-code token lifetime.
- **Obtaining a new token automatically invalidates previously issued tokens.**
- Centralize token refresh so multiple workers do not independently refresh and invalidate one another's tokens.

### Scopes required by this implementation

| Operation | Scope |
|---|---|
| P2P bulk send | `BulkApiAccess` |
| Get DLR | `BulkApiAccess` or `ApiAccess` |

For this workflow, using a `BulkApiAccess` bearer token for both P2P bulk sending and DLR retrieval is the cleanest choice supported by the document.

### Bearer request header

```http
Authorization: Bearer <access_token>
```

## 2.3 API Key

API keys are created in the Asiatech SMS panel under API key settings.

Header:

```http
X-API-Key: <your-api-key>
```

The Persian PDF's extracted RTL text may render this header backward as `Key-API-X`; the visual document shows the intended API-key header naming convention as `X-API-Key`.

## 2.4 Production IP permission

For operational access, provide the source IP address to Asiatech so it can be permitted for the account. An unapproved/blocked IP can produce result code `2405` (`IP Address is Blocked`).

---

# 3. Rate Limits and Request Validation

Two independent limits matter.

## 3.1 MPS — Message Parts Per Second

MPS is the number of **SMS parts** that can be sent per second.

- MPS is account-specific.
- It can be viewed with the `UserInfo` endpoint.
- Asiatech may change/increase it by coordination with their business team.
- A multipart SMS consumes multiple parts and therefore more MPS capacity than a one-part SMS.

## 3.2 RPS — Requests Per Second

| Method/category | Maximum |
|---|---:|
| GET DLR | **10 RPS** |
| SEND | **100 RPS** |
| OTP | **100 RPS** |
| WebEngage | **200 RPS** |
| Other methods, total | **100 RPS** |

If the request rate is exceeded:

```text
HTTP 429
ResultCode 118
Message: Rate Limit Exceeded
```

## 3.3 Strict request validation

Asiatech validates the entire JSON request body before processing.

If any field/item is invalid — including:

- unsupported field
- incorrect value
- wrong data type
- malformed structure

—the **entire request is rejected** with HTTP `400 Bad Request`.

### Mandatory implementation rule

For optional properties that have no value:

> **Do not send them as `null` or empty values. Omit them from the JSON body entirely.**

This is particularly important for batch P2P requests: one invalid item may reject the complete request.

---

# 4. Send Bulk P2P Messages

## 4.1 Endpoint

```http
POST https://smsapi.asiatech.ir/api/{apiversion}/message/P2PBulk
Content-Type: application/json
Authorization: Bearer <token>
```

Scope:

```text
BulkApiAccess
```

Recommended explicit version:

```text
https://smsapi.asiatech.ir/api/4/message/P2PBulk
```

## 4.2 Request body

The request is a JSON array. Each element represents one P2P SMS message.

### Message fields

| Field | Type | Required | Description |
|---|---|---:|---|
| `SourceAddress` | string | Yes | Sender number |
| `DestinationAddress` | string | Yes | Recipient number |
| `MessageText` | string | Yes | SMS content |
| `ValidityPeriod` | ISO-8601 datetime string | No | Message-validity expiration time |
| `TargetUDH` | array/list | No | UDH reference-number configuration |
| `udh` | string | No | User-defined extra data / correlation value |

### P2P example

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

## 4.3 `TargetUDH`

Allowed reference-number types:

```json
["referenceNumberType:8bit"]
```

or:

```json
["referenceNumberType:16bit"]
```

The field is an array of strings and may contain only **one** member.

For a large number of messages sent from one source number to the same destination number in one day, Asiatech recommends **16-bit reference numbers** to reduce display/concatenation problems on the recipient handset.

## 4.4 `ValidityPeriod`

According to the source:

- The validity period must not be less than **1 hour** from the current time.
- It must not be more than **4 hours** from the current time.
- Use of this parameter requires coordination with Asiatech's technical team.

If not required, omit it entirely.

## 4.5 `udh`

`udh` may be used as user-defined extra data, such as an internal campaign/correlation identifier.

Important:

- Asiatech does **not** enforce uniqueness for this value.
- Reusing the same `udh` value across messages is allowed.
- DLR can optionally return this value using `returnUDH=true`.

## 4.6 Response

Common wrapper:

| Field | Type |
|---|---|
| `message` | string |
| `succeeded` | boolean |
| `data` | response data |
| `resultCode` | integer |

The P2P section shows the following successful response shape:

```json
{
  "message": "Successfully done.",
  "succeeded": true,
  "data": [
    {
      "id": "6820371f9114e1afc0580c5d",
      "part": "1",
      "upstreamGateway": "Rightel"
    },
    {
      "id": "6820371f9114e1afc0580c5e",
      "part": "1",
      "upstreamGateway": "SMTN"
    }
  ],
  "resultCode": 100
}
```

### Persist immediately

For every accepted message persist at least:

```text
provider_message_id = data[].id
part_count          = data[].part
upstream_gateway    = data[].upstreamGateway
source_address
destination_address
message_text / local message ID
udh (if supplied)
sent_at
```

The returned `id` is the key required for later DLR requests.

## 4.7 Gateway/operator interpretation

Asiatech documents these upstream names:

- `MCI`, `2SMCI`, `3SMCI`, `SMCI` → MCI / Hamrah-e Aval
- `MTN`, `SMTN` → MTN Irancell
- `ApTel` → ApTel
- `RighTel` → Rightel

Do not assume every possible `upstreamGateway` value is listed; retain the raw value in the database.

---

# 5. Retrieve Delivery Status (DLR) Repeatedly

## 5.1 Endpoint

```http
POST https://smsapi.asiatech.ir/api/message/getdlr
Content-Type: application/json
Authorization: Bearer <token>
```

Supported scope:

```text
ApiAccess or BulkApiAccess
```

Rate limit:

```text
10 requests/second
```

A single call can query up to **1000 message IDs**.

## 5.2 Request body

The body is a JSON array of Asiatech message IDs:

```json
[
  "624ed1bbcd0efb1ef148e0a0",
  "624ed1bbcd0efb1ef148e0a1"
]
```

## 5.3 Response

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
    },
    {
      "id": "624ed1bbcd0efb1ef148e0a1",
      "partStatus": [
        {
          "item1": 1,
          "item2": 1,
          "item3": "2022-04-07T11:57:50.043Z"
        }
      ],
      "deliveryStatus": 1
    }
  ],
  "resultCode": 100
}
```

### `partStatus` meaning

| Field | Meaning |
|---|---|
| `item1` | SMS part sequence number |
| `item2` | Delivery status code for that part |
| `item3` | Timestamp at which the upstream operator status was received |

### `deliveryStatus`

`deliveryStatus` is the overall status of the complete message.

### Store every poll

Because delivery state may change, do not overwrite all historical evidence with only the newest value. At minimum maintain:

- latest overall status
- latest status per part
- first seen timestamp
- last seen timestamp
- provider status timestamp (`item3`)
- DLR polling timestamp

For auditability/billing reconciliation, storing a DLR history table is preferable.

## 5.4 Optional DLR response parameters

### Long IDs

If sending used:

```text
?returnLongId=true
```

then DLR must also use:

```text
https://smsapi.asiatech.ir/api/message/getdlr?returnLongId=true
```

Keep the send and DLR ID mode consistent.

### Return `udh`

If a `udh` value was assigned when sending:

```text
https://smsapi.asiatech.ir/api/message/getdlr?returnUDH=true
```

### Return send/process timestamps

To receive message creation/processing time at Asiatech and the time it was sent to the upstream operator:

```text
https://smsapi.asiatech.ir/api/message/getdlr?returnSentDate=true
```

## 5.5 Critical multipart status behavior

Asiatech explicitly documents a delayed correction rule:

If a multipart message is initially overall `Delivered`, but:

- at least one part is `Delivered`, and
- one or more remaining parts are `Undeliverable` or `Expired`,

then after **20 minutes from the last received DLR**, Asiatech changes the overall message status to:

```text
Enroute (33)
```

Therefore:

> **A `Delivered` result should not automatically be considered immutable/final immediately.**

The backend must poll these messages again after the documented finalization window.

---

# 6. Recommended DLR Polling Strategy

This schedule is an implementation strategy derived from Asiatech's documented status-finalization behavior; the exact polling intervals other than the documented 20-minute/7-hour boundaries are not prescribed by the PDF.

For each successfully submitted message ID:

1. Query shortly after submission for early state.
2. Continue polling while the status is non-final.
3. For `Delivered`, perform a confirmation query after the 20-minute finalization boundary.
4. For `Sent (9)` and `Undeliverable (34)`, continue until the documented 7-hour boundary has passed.
5. Stop repeated polling once the status is final according to the finalization table below.

A practical schedule could be:

```text
+1 min
+5 min
+20–25 min
+1 h
+3 h
+7 h+
```

but this schedule is an application choice, not an Asiatech-mandated schedule.

### Batch polling

- Max IDs per DLR request: **1000**
- Max DLR request rate: **10 RPS**

Batch pending IDs rather than querying one ID per request.

---

# 7. User Info / Credit / MPS

## Endpoint

```http
GET https://smsapi.asiatech.ir/api/user/userinfo
Authorization: Bearer <token>
Content-Type: application/json
```

The document states that this endpoint returns account information including credit, sender numbers, and send limits.

Example:

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

Important fields:

| Field | Meaning |
|---|---|
| `credit` | Current account credit |
| `userPaymentType` | e.g. `PrePaid` |
| `mps` | Current SMS-part throughput allocation |
| `senderIds` | Allowed sender IDs/numbers |

The source does not specify a separate scope for `UserInfo`; use an authentication method accepted by the endpoint and handle `Mismatched Scope (138)` if a bearer scope is not accepted in a particular account configuration.

---

# 8. Ping / Health Check

## Endpoint

```http
GET https://smsapi.asiatech.ir/api/Tools/Ping
Authorization: Bearer <token>
```

Successful accessible response:

```text
PONG
```

Use this to distinguish general API reachability/auth/network problems from send-specific errors.

---

# 9. ALL API Response Codes

These are API-level response/result codes from the source document.

| HTTP | Result Code | Message | Meaning / Description |
|---:|---:|---|---|
| 200 | 100 | `Successfully done.` | Successful request |
| 500 | 101 | `Database Error` | Database error |
| 500 | 102 | `Repository Error` | Repository error |
| 400 | 103 | `Model Error` | Request/body model error |
| 504 | 104 | `Api Connection Attempt Failure` | API connection failure |
| 503 | 105 | `Service Un-Available` | Service unavailable |
| 403 | 106 | `Email Confirm Failed` | Email not confirmed |
| 404 | 107 | `User Not Found` | User not found |
| 409 | 108 | `Duplicate Error` | Duplicate item |
| 200 | 109 | `Your IDs is not available now!` | Requested item/ID not found/available now |
| 500 | 110 | `Unable To Create Result Api Error` | Unable to create API result |
| 500 | 111 | `Auto Mapper Error` | Mapping error |
| 500 | 112 | `General Failure` | General failure |
| 502 | 113 | `Web Service Error` | Third-party web-service error |
| 500 | 114 | `Error Getting Token from Idp` | Error obtaining token from identity provider |
| 500 | 115 | `Error Retrieving Data from AppSettings` | Error reading application settings |
| 400 | 116 | `Header Error` | Request header is incorrect/misconfigured |
| 429 | 118 | `Rate Limit Exceeded` | Request/send rate limit exceeded |
| 403 | 119 | `User Tariff Null` | User has no tariff |
| 403 | 122 | `Send To Whitelist Only` | Account may send only to whitelist |
| 400 | 123 | `Enter Whitelist Template` | Whitelist template name required |
| 400 | 124 | `Enter Whitelist Message Text` | Whitelist message text required |
| 404 | 125 | `Whitelist Template Not Found` | Whitelist template name is invalid/not found |
| 400 | 126 | `Source Address Not Valid` | Sender/source number invalid |
| 400 | 127 | `Detination Address Not Valid` | Destination/alternate sender address invalid (spelling preserved from source) |
| 400 | 128 | `Item is not activated` | Item is not active |
| 400 | 129 | `Item is not deactivated` | Source wording preserved |
| 401 | 130 | `Authorize` | Not authorized |
| 400 | 131 | `Date Time Not Valid` | Date/time invalid |
| 403 | 132 | `Send By Special Parameter Source Address Not Allowed` | Special-parameter sender/source address not allowed |
| 404 | 133 | `Send By Special Parameter Enter Destination Address` | Special-parameter destination required |
| 404 | 134 | `Send By Special Parameter Destination Address Not Found` | Special-parameter destination not found |
| 402 | 135 | `Not Enough Credit` | Insufficient credit |
| 400 | 136 | `Invalid Message Id` | Message ID invalid |
| 409 | 137 | `Exist Item` | Item already exists |
| 403 | 138 | `Mismatched Scope` | Scope missing or incorrect for endpoint |
| 403 | 2403 | `The endpoint is not accessible` | Account has no access to endpoint |
| 403 | 2404 | `This EndPoint is not for Bulk Messagges` | High-rate/bulk traffic sent to a non-bulk endpoint |
| 403 | 2405 | `IP Address is Blocked` | Source IP blocked/not permitted |
| 401 | 2406 | `Unauthorized` | Missing Authorization header, incorrect username/password, or incorrect API key |
| 405 | 2407 | `Method Not Allowed` | Wrong HTTP method for endpoint |
| 413 | 2408 | `Request body too large` | Request body exceeds allowed size |
| 403 | 2409 | `user-agent is blocked` | User-Agent header/value blocked |
| 401 | 2410 | `invalid_token` | Bearer token invalid |
| 401 | 2411 | `The token expired at ...` | Bearer token expired |
| 400 | 2412 | `Request rejected: Invalid content or length.` | OTP content/length does not comply with OTP rules |

### Backend rule

Do not branch only on HTTP status. Parse and log:

```text
http_status
resultCode
message
succeeded
raw_response
```

---

# 10. ALL Send Error Codes

The source lists the following **send-stage ErrorCode values**. Every row in this table is shown with:

```text
HTTP Code = 200
Result Code = 100
```

Therefore, these codes represent a distinct send-level outcome and must not be conflated with API transport/result codes.

| ErrorCode | Error Message | Meaning |
|---:|---|---|
| 0 | `Send Error` | Send error |
| -1 | `Not Enough Credit` | Insufficient credit |
| -2 | `Server Error` | Server error |
| -3 | `DE ACTIVE Account` | Account is deactivated |
| -4 | `Expired Account` | Account expired |
| -5 | `Invalid Username or Password` | Invalid username or password |
| -6 | `Authentication Failure` | Authentication failure |
| -7 | `Server Busy` | Server busy |
| -8 | `Number At Backlist` | Recipient is blacklisted |
| -9 | `Limited In Send Day` | Daily send limit reached |
| -10 | `Limited In Volume` | Send-volume/speed limit reached |
| -11 | `Invalid Sender Number` | Invalid sender number |
| -12 | `Invalid Receiver Number` | Invalid receiver number |
| -13 | `Invalid Destination Network` | Invalid recipient operator/network |
| -14 | `Unreachable Network` | Network unreachable |
| -15 | `DE ACTIVE Sender Number` | Sender number deactivated |
| -16 | `Invalid Format of Sender Number` | Sender-number format invalid |
| -17 | `Tariff Not Found` | No tariff exists for this send |
| -18 | `Invalid Ip Address` | Source IP invalid |
| -19 | `Invalid Pattern` | Pattern/template invalid |
| -20 | `Expired Sender Number` | Sender number expired |
| -21 | `Message Contains Link` | Message contains a link |
| -22 | `Invalid Port` | Invalid port |
| -23 | `Message Too Long` | Message too long |
| -24 | `Filter Word` | Message contains filtered word(s) |
| -25 | `Invalid Reference Number Type` | Invalid reference-number type |
| -26 | `Invalid Target UDH` | Invalid TargetUDH data |
| -27 | `Limited In Send Month` | Monthly send limit reached |
| -28 | `Data Coding Not Allowed` | Data coding not allowed |
| -29 | `Not Found Route` | No route found for send |
| -30 | `Message Contains Scripts` | Message contains script content |
| -31 | `Setting Not Found` | Required send setting not found |
| -32 | `Content Filter` | Content is filtered |
| -33 | `Invalid Character` | Message contains invalid character(s) |
| -34 | `SMS Wallet Not Enough Credit` | SMS wallet has insufficient credit |
| -35 | `Campaign Exist` | Campaign name already exists |
| -36 | `Approver Not Found` | Message approver not found |
| -37 | `Due Date Less Then Current Date` | Due date earlier than current date |
| -38 | `Invalid Message Request Id` | Invalid message-request ID |
| -39 | `Message Request Not Waiting For Approve` | Request is not waiting for approval |
| -40 | `Expire Date Less Then Due Date` | Expiry date earlier than due date |
| -41 | `Limited In Schedule` | Scheduled-message limit reached |
| -42 | `Too Many Request` | Duplicate / too many request condition (Persian description states duplicate message send) |
| -43 | `Cache Server Error` | Cache server error |

### Important source limitation

The PDF provides the send error-code table but does **not** clearly specify, in the P2P success-response example, the exact JSON field/path in which these `ErrorCode` values are returned for every P2P failure mode. Preserve/log the complete raw response so these codes can be captured wherever Asiatech exposes them.

---

# 11. ALL Delivery Status Codes

These codes may appear in `partStatus[].item2` and/or `deliveryStatus`.

| Code | Status | Meaning |
|---:|---|---|
| 1 | `Delivered` | Delivered to handset |
| 2 | `UnDelivered` | Not delivered to handset |
| 3 | `Accepted` | Received/accepted by operator |
| 4 | `ReceivedByUpstream` | Received by upstream service provider |
| 5 | `Rejected` | Rejected by upstream service provider |
| 6 | `NotReceiveByServer` | SMS not received by server |
| 7 | `ErrorInSending` | Error occurred while sending |
| 8 | `WaitingForSend` | Waiting to send |
| 9 | `Sent` | Sent |
| 10 | `NotSent` | Not sent |
| 11 | `Expired` | Expired |
| 12 | `IsSending` | Sending in progress |
| 13 | `IsCanceled` | Canceled |
| 14 | `BlackList` | Blacklisted |
| 15 | `SmsIsFilter` | SMS text is filtered |
| 16 | `Deleted` | Deleted |
| 17 | `WaitingForConfirmation` | Waiting for confirmation |
| 18 | `NotEnoughBalance` | Insufficient balance |
| 19 | `IsPreparing` | Preparing |
| 20 | `IsPreparedForSending` | Prepared for sending |
| 21 | `AccessDenied` | Access denied |
| 22 | `TextIsEmpty` | Message text empty |
| 23 | `InvalidInputFormat` | Invalid input format |
| 24 | `InvalidUserOrPassword` | Invalid user/password |
| 25 | `InvalidUsedMethod` | Invalid method used |
| 26 | `InvalidSender` | Invalid sender |
| 27 | `InvalidMobile` | Invalid mobile number |
| 28 | `InvalidReception` | No recipient specified |
| 29 | `Stored` | Stored |
| 30 | `BlackListTable` | Number exists in blacklist table |
| 31 | `GetDeliveryStatus` | Requesting latest delivery status |
| 32 | `Unknown` | Unknown |
| 33 | `Enroute` | In route; final state not yet determined |
| 34 | `Undeliverable` | Undeliverable |
| 35 | `MessageQueueFull` | Message queue full |
| 36 | `UnreachableNetwork` | Network unreachable |

**Do not treat all 36 statuses as terminal.** Use the finalization table below.

---

# 12. Status Finalization Times

This is the source table for when status should be considered final, both per part and for the overall `deliveryStatus`.

| Code | Status | Final per part? | Final overall (`deliveryStatus`)? |
|---:|---|---|---|
| 1 | `Delivered` | Yes | **After 20 minutes from send time** |
| 2 | `UnDelivered` | Yes | Yes |
| 3 | `Accepted` | Yes | Yes |
| 5 | `Rejected` | Yes | Yes |
| 7 | `ErrorInSending` | Yes | Yes |
| 9 | `Sent` | **No — until 7 hours after send time** | **No — until 7 hours after send time** |
| 10 | `NotSent` | Yes | Yes |
| 11 | `Expired` | Yes | Yes |
| 16 | `Deleted` | Yes | Yes |
| 32 | `Unknown` | Yes | Yes |
| 33 | `Enroute` | Yes | Yes |
| 34 | `Undeliverable` | **No — until 7 hours after send time** | **No — until 7 hours after send time** |
| 36 | `UnreachableNetwork` | Yes | Yes |

### Consequences for repeated polling

- `Delivered (1)` must receive a later confirmation because the overall status is not final until the documented 20-minute boundary.
- `Sent (9)` must remain pollable until the 7-hour boundary.
- `Undeliverable (34)` must remain pollable until the 7-hour boundary.
- For multipart messages, also apply the separate rule that a `Delivered` overall result may be revised to `Enroute` after 20 minutes from the last DLR if some parts are delivered and others are expired/undeliverable.

---

# 13. Chargeback Rules by Operator, Per SMS Part

Chargeback is calculated **per SMS part**, not only per logical message.

Legend:

- **Charged** = cost remains deducted
- **Chargeback** = deducted cost is returned to account credit

| Code | Status | MCI / Hamrah-e Aval | Irancell | Other operators |
|---:|---|---|---|---|
| 1 | `Delivered` | Charged | Charged | Charged |
| 2 | `UnDelivered` | **Chargeback** | **Chargeback** | Charged |
| 3 | `Accepted` | Charged | Charged | Charged |
| 5 | `Rejected` | **Chargeback** | **Chargeback** | Charged |
| 7 | `ErrorInSending` | **Chargeback** | **Chargeback** | **Chargeback** |
| 9 | `Sent` | Charged | Charged | Charged |
| 10 | `NotSent` | **Chargeback** | **Chargeback** | **Chargeback** |
| 11 | `Expired` | **Chargeback** | **Chargeback** | Charged |
| 16 | `Deleted` | **Chargeback** | **Chargeback** | Charged |
| 32 | `Unknown` | **Chargeback** | **Chargeback** | Charged |
| 33 | `Enroute` | **Chargeback** | **Chargeback** | Charged |
| 34 | `Undeliverable` | **Chargeback** | **Chargeback** | Charged |
| 36 | `UnreachableNetwork` | **Chargeback** | **Chargeback** | Charged |

## Multipart `Enroute` chargeback rule

For a multipart message whose overall status becomes `Enroute` because:

- at least one part is `Delivered`, and
- the remaining part(s) are `Undeliverable` and/or `Expired`,

Asiatech states that the fees charged for the parts whose statuses are `Undeliverable` or `Expired` are calculated as chargeback and returned to the user's credit.

This means billing reconciliation should be done at the **part level** whenever possible.

---

# 14. Required Backend State Model

A robust implementation should retain enough state to send once and poll many times safely.

## Send record

```text
local_message_id
provider = asiatech
provider_message_id
source_address
destination_address
message_text
udh
part_count
upstream_gateway
submitted_at
api_version
raw_send_response
```

## DLR snapshot/history record

```text
provider_message_id
polled_at
part_number
part_status_code
part_status_name
provider_status_at
overall_delivery_status_code
overall_delivery_status_name
is_final
raw_dlr_response
```

## Billing/reconciliation fields

```text
operator_group = MCI | IRANCELL | OTHER
part_count
chargeback_eligible_parts
latest_credit
```

Do not derive the operator only once from the phone prefix if Asiatech returns `upstreamGateway`; number portability/routing makes provider-returned routing metadata preferable for reconciliation.

---

# 15. Implementation Checklist

- [ ] Production IP is permitted by Asiatech.
- [ ] Authentication credentials/secrets are stored outside source code.
- [ ] Bearer token refresh is centralized.
- [ ] Token expiry comes from `expires_at` / `expires_in`, not a hard-coded lifetime.
- [ ] `BulkApiAccess` scope is used for P2P bulk send.
- [ ] Explicit API version is used; v4 is preferred for this workflow.
- [ ] Optional empty/null fields are omitted from JSON.
- [ ] P2P payload is validated before sending because one invalid item can reject the whole request.
- [ ] RPS limiter is implemented.
- [ ] MPS limiter counts **parts**, not messages.
- [ ] Every returned Asiatech message `id` is persisted.
- [ ] `part` and `upstreamGateway` are persisted when returned.
- [ ] DLR requests are batched to <= 1000 IDs.
- [ ] DLR polling is limited to <= 10 RPS.
- [ ] `partStatus.item1`, `item2`, and `item3` are persisted.
- [ ] Overall `deliveryStatus` is persisted separately from per-part status.
- [ ] `Delivered` is rechecked after its finalization window.
- [ ] `Sent` and `Undeliverable` remain pollable through the 7-hour boundary.
- [ ] Multipart Delivered→Enroute behavior is supported.
- [ ] API result codes, send error codes, and delivery-status codes are modeled as three different code systems.
- [ ] Raw send/DLR responses are retained for debugging and reconciliation.
- [ ] Chargeback is reconciled per part and operator group.
- [ ] Account `credit` and current `mps` can be read from `UserInfo`.
- [ ] `Ping` is available for service health checks.

---

# 16. Minimal End-to-End Flow

```text
1. Acquire BulkApiAccess token
        |
2. GET UserInfo -> credit, mps, senderIds
        |
3. Validate P2P batch locally
        |
4. Apply RPS + MPS throttling
        |
5. POST /api/4/message/P2PBulk
        |
6. Persist every returned message id / part / gateway
        |
7. Queue message IDs for DLR polling
        |
8. POST /api/message/getdlr (<=1000 IDs, <=10 RPS)
        |
9. Persist overall + per-part status
        |
10. Determine final/non-final using the finalization table
        |
11. Requeue non-final messages for later DLR polling
        |
12. Stop only when final
        |
13. Reconcile per-part chargeback by status + operator
```

