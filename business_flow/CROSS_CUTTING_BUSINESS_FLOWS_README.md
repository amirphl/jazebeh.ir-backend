# Cross-cutting business flows

## Purpose and relation to the primary guides

This guide covers the non-`bundle_`, `campaign_`, and `smart_` flows that change whether a campaign can be priced, approved, delivered, measured, or compensated. Read it with the bundle, Smart Targeting, campaign-lifecycle, approval/delivery, and scheduler guides; it intentionally records shared contracts rather than duplicating their domain logic.

The application and PostgreSQL database each run as one instance. The review focus is therefore transactional integrity, retry/idempotency within that instance, and correct handling of external services. Replication and single-point-of-failure concerns are excluded.

```text
Platform settings + line number + media + audience spec
                     + price tables
                              |
                              v
customer campaign create/update/cost/approval ──> platform scheduler
       |                                             |
       v                                             v
wallet funding / balance snapshots              short links -> visits -> reports
       ^                                             |
       +---------------- refund reconciliation ------+

Audit logging records administrative and money-sensitive mutations throughout.
```

## 1. Wallet funding, settlement, and campaign money

### Card-payment flow

`PaymentFlow.ChargeWallet` validates the customer and amount, calculates tax and agency/system settlement shares, and creates a pending `payment_requests` record before it requests an Atipay token. The saved request is the local correlation point for the redirect/callback; it must exist before the customer can pay.

`PaymentCallback` validates callback fields, opens one transaction, and makes these repository operations in order:

| Repository function/query | Query intention and business role |
| --- | --- |
| `PaymentRequestRepository.ByInvoiceNumber(reservationNumber)` | Select the one local payment request identified by the merchant invoice/reservation number. |
| customer lookup | Confirms the payment request's owner still exists and provides audit ownership. |
| `PaymentRequestRepository.Update` | Changes the pending request to the provider-mapped status and stores provider reference/trace/PAN fields for a successful callback. |
| Atipay verification API | Independently verifies the provider reference and amount before any wallet credit. This is an external read, not a repository query. |
| wallet/balance reads (`ByCustomerID`, `GetCurrentBalance` and special-wallet helpers) | Load customer, agency, tax, and system wallets and their latest balance snapshots. |
| agency-discount lookup | Loads the settlement rule captured in payment metadata. |
| balance-snapshot and transaction writes | Append the resulting wallet balances and ledger transactions, so campaign freeze/release logic consumes an auditable balance history rather than a mutable number alone. |

The callback accepts only a pending, unexpired request. On a successful provider status it verifies the amount in rials against the saved toman amount, then credits all settlement parties in the same database transaction. Verification/credit errors intentionally convert the request to failed and commit that result rather than rolling back to `pending`; otherwise the same callback could repeatedly attempt a non-recoverable credit.

Customer history and balance views use `TransactionRepository.ByCustomerID`/`GetHistoryWithMetadata` and `WalletRepository.GetCurrentBalance` to read the ledger/snapshot result. Deposit-receipt operations use the receipt and multimedia repositories to store a receipt document and admin evidence; `LockCustomerInvoiceUUID` serializes assigning a customer invoice UUID, and `IsCustomerDepositInvoiceUUIDAlreadyLinked` prevents reuse.

### Administrative and crypto funding

`AdminChargeWallet` first checks `FindAdminChargeByIdempotencyKey`; the query returns the already-created request for a retried admin command. A new charge writes its payment request, transaction(s), and balance snapshot(s) atomically and logs the admin action. `AdminUpdateDepositReceiptStatus` is the manual approval/rejection path; its repository reads/writes move a receipt state and, on approval, apply the same ledger discipline.

`CryptoPaymentFlow.CreateRequest` creates a `crypto_payment_requests` record and asks OxaPay for an address/invoice. `GetStatus` reads the local request/deposits and can refresh from the provider; `ManualVerify` is an operator-triggered provider verification; `CancelRequest` changes only a still-cancellable local request.

`HandleOxapayWebhook` verifies an HMAC over the raw body, ignores unrelated types, and runs one transaction:

| Repository function/query | Query intention and business role |
| --- | --- |
| `CryptoPaymentRequestRepository.ByProviderRequestID(trackID)` | Find the request by the provider's authoritative correlation key. |
| `ByDepositAddress(address, memo)` | Fallback lookup for provider callbacks lacking a track ID. |
| crypto-deposit `ByTxHash` / `Save` / `Update` | Upsert every blockchain transaction so duplicate webhooks update confirmations rather than create a second deposit. |
| crypto-request `Update` | Persist mapped invoice status/reason. |
| `creditOnConfirmed` wallet, snapshot, transaction, and discount queries | Once-only credit path, guarded by both deposit and request `CreditedAt` checks. |

The important boundary is that provider confirmation becomes a local deposit first; wallet credit is a subsequent, guarded transaction. Campaign funding and later refunds therefore share the same wallet/snapshot/transaction accounting model.

## 2. Pricing, sender eligibility, and audience vocabulary

These flows are the configuration inputs to campaign validation, cost calculation, Smart Targeting eligibility, test sending, and scheduler dispatch.

| Flow | Repository functions/queries | Business intention |
| --- | --- | --- |
| Platform settings | `PlatformSettingsRepository.ByFilter`, `Count`, `UpdateStatus`, `AppendMetadata`; multimedia `ByUUID` | A customer creates a platform sender/settings record, usually referencing approved media. Admin status makes a sender usable or unusable; metadata stores administrative provider details without replacing the whole record. Campaigns must use an eligible/approved setting. |
| Platform base price | `PlatformBasePriceRepository.List`, `LatestByPlatform`, `UpdatePriceByPlatform` | Reads current platform base price for customer pricing; admin update changes the platform's current price record/value. Cost calculations combine this with segment factors. |
| Segment price factors | `ListLatestByLevel3[ForPlatform]`, `LatestByLevel3s[ForPlatform]`, `ByFilter` | Selects the latest factor per audience level-3 segment, optionally platform-specific. Campaign costing queries the factors for its audience segments, while admin creation adds a future/current factor record. |
| Line numbers | `LineNumberRepository.ByFilter`, `ByValue`, `Update`, `UpdateBatch` | Customer list exposes active sender numbers; admin CRUD and batch update control status, platform, colors, and price factor. The number/color is an input to line-number eligibility in delivery and Smart Targeting. |
| Audience specification | `AudienceSpecRepository.ListActive` | Reads active reference-data rows and maps them into the level-1/2/3 hierarchy and valid filter options. `CampaignFlow.cachedAudienceSpec` caches this read; campaign input and factor administration must be validated against the same vocabulary. |

The critical logic occurs between these reads. A campaign's calculated cost must be based on the same platform, segment factors, and sender constraints later used at approval/delivery. A line-number color change can shrink the delivery-eligible Smart Targeting population; price and eligibility are related inputs but do not themselves reserve an audience. Reservations/materialization in the campaign/Smart Targeting flows are the commit boundary.

## 3. Multimedia ownership and worker access

Customer `UploadMultimedia` validates ownership/customer context and media payload, then persists the asset through `MultimediaAssetRepository` (existence/UUID and customer-filter queries). Customer `DownloadMultimedia` and `PreviewMultimedia` first query `ByUUID`, require `CustomerID` ownership, and return stored content/metadata.

Admin upload/download bypasses customer ownership only after administrative authorization; bot download queries `ByUUID` for a campaign worker. Platform settings and campaign/test-send validation use the same asset lookup to ensure a referenced media UUID is real and suitable before an external provider receives it. This gives the scheduler a stable file identity rather than accepting arbitrary worker URLs.

## 4. Short-link allocation, publication, visit attribution, and reports

### Campaign/bot allocation

`BotShortLinkFlow.GenerateAndCreateShortLinks` allocates recipient-specific links for campaign/test messages. It persists mapping rows before publication. `ShortLinkRepository.SaveBatch` performs a bulk insert (using PostgreSQL `COPY` for the optimized path); the stored mapping binds UID, long URL, campaign/client/scenario data, recipient information, and test flag.

`publishShortLinkMappings` provides the send-safety sequence:

1. It validates the new UIDs and calls `ByUIDs`, which re-queries the rows actually committed in the database.
2. It calls the external mapping publisher with those persisted rows.
3. Only after external acknowledgement does it call `MarkExternallyPublished(uids, time)`. If that marker write fails, later publishing can repeat the external upload; the external contract must therefore be idempotent by UID.

Background publication uses `ListPendingExternalPublication(limit)` to select mappings lacking the published marker. This fills the gap between successful local allocation and a failed/interrupted external publication, before a campaign's links are relied upon by recipients.

### Admin CSV allocation

`AdminShortLinkFlow.CreateShortLinksFromCSV` creates an upload job and attempts it promptly. `ProcessPendingUploadJobs` later calls `AdminShortLinkUploadJobRepository.Due(now, 20)`, which selects pending/retrying jobs due for retry. `Claim(id, now, lease)` atomically changes one job to processing and records an expiry; `UpdateClaimed` only persists the worker that still owns that claim.

For a claimed job, the flow validates the CSV `long_link` column and pre-counts valid rows. `ByAllocationKey` is the idempotency lookup: a retried job reuses already allocated links. If absent, one database transaction calls `ReserveSequentialUIDs(count)`—which serializes the fixed-width UID allocator—and `PersistAllocation`, inserting the exact link batch and progress record together. Publication occurs in batches of 500 through the same persisted-row/acknowledgement sequence. Transient failures set exponential `NextAttemptAt`; invalid CSV, UID exhaustion, or too many attempts becomes terminal failure.

### Public redirect and analytics

`ShortLinkVisitFlow.Visit` is unauthenticated. `ShortLinkRepository.ByUID(uid)` resolves the mapping, then `ShortLinkClickRepository.Save` inserts an immutable click event containing copied link/campaign/scenario/recipient/test fields plus optional IP and user agent. The redirect is returned only after that save succeeds. Reporting queries such as `ListWithClicksDetailsByScenario`, `...ByScenarioRange`, and `DistinctShortLinkUIDsByCampaignID[s]` join/group those immutable events for campaign click reports.

Copying link fields into the click row is deliberate: a later mapping change cannot rewrite historical attribution. Campaign click reports consume this data downstream of sending; they do not imply that a click was delivered or that a refund is due.

## 5. Shared audit and request context

`ClientMetadata` carries request, session, device, additional, and location fields through flow calls. `logAdminAction` extracts the authenticated admin ID, target customer, endpoint, request ID, IP, user agent, action result, and error, serializes metadata, and calls `AuditLogRepository.Save`.

Audit read queries—`ListByCustomer`, `ListByAction`, `ListFailedActions`, `ListSecurityEvents`, and filtered/count queries—are operational evidence, not the authority for a business transition. Important transitions must persist their domain state first or transactionally with it; audit writes are best-effort in several flows and should not be assumed to compensate for a missing ledger/status update.

## Logical bugs and issues to review

1. **High — external provider calls occur inside database transactions in payment callbacks.** `PaymentCallback` opens its transaction before `verifyPaymentWithAtipay`. Provider latency holds database transaction resources while no local state can progress; a timeout/uncertain provider result must be handled carefully to avoid a pending request that has actually settled externally.
2. **High — payment and crypto settlement metadata uses unchecked JSON type assertions.** `updateBalances` and `creditOnConfirmed` access decoded fields with expressions such as `m["amount_with_tax"].(float64)`. Malformed, historical, or partially migrated metadata can panic rather than yield a controlled failed payment state.
3. **High — external short-link publication is at-least-once.** The marker is written after the external upload, and its error is ignored. Retrying is intentional and safe only if the external publisher treats UID mapping uploads as idempotent replacements; otherwise duplicate publication is possible after an acknowledgement/marker failure.
4. **Medium — short-link redirect depends on analytics persistence.** A database failure in `ShortLinkClickRepository.Save` prevents returning the destination URL. This gives complete recorded clicks but turns analytics-table availability into user-visible redirect availability.
5. **Medium — public visit flow records supplied IP/user-agent without validation or trust-boundary normalization.** If endpoint middleware does not derive the client IP from trusted proxy configuration, audit/report data can be spoofed and privacy retention becomes harder to reason about.
6. **Medium — configuration freshness is not a reservation.** Prices, line-number status/colors, and audience specification are read independently and may change after a cost preview. Approval and the Smart Targeting reservation/materialization path must revalidate the relevant inputs; otherwise an approved price or eligible population can be stale.
7. **Low — audit logging is best effort.** `logAdminAction` discards JSON marshal/repository errors. A successful mutation can lack an audit row, so compliance/operations must not use audit presence as the only proof that a transition happened.

## Bottlenecks and operational pressure points

1. **Wallet settlement fans out writes.** One confirmed funding event reads multiple wallets and latest snapshots, then writes several snapshots/transactions. Concurrent payments contend on the same customer, agency, tax, and system accounting rows/tables.
2. **Provider latency holds payment transactions open.** Card verification inside the callback transaction magnifies connection/lock pressure during provider slowness.
3. **Short-link allocation is intentionally serialized.** `ReserveSequentialUIDs` protects collision-free sequence allocation. Large CSV jobs wait on that allocator and then bulk-insert/publish potentially many rows.
4. **Publication and reporting scale linearly with links/clicks.** Batches of 500 cap external payloads, but large scenarios cause many database reloads, uploads, click inserts, and report joins. Indexes on UID, campaign/scenario IDs, allocation key, publication marker, and click foreign keys are essential.
5. **Audience-spec cache refresh and price-factor lookups can become hot reads.** Cache misses or frequent configuration edits cause repeated reference-data and latest-per-segment queries during campaign previews and validation.
6. **Binary media is loaded through application flows.** Large preview/download payloads consume process memory and database/storage bandwidth and can contend with delivery workers retrieving campaign media.

