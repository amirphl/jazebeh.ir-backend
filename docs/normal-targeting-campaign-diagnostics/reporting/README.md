# Reporting queries

These queries describe selected audiences, remaining capacity, scheduler
reduction stages, and financial audit reports. Audience reports current
database state and do not reconstruct a historical snapshot of mutable
audience profile fields. Financial reports use the immutable `transactions`
ledger and the balance snapshots embedded in each transaction.

See the [diagnostics index](../README.md) for query descriptions and shared
assumptions.

## Financial reporting

Each financial query has a `params` CTE. Replace `from_at` with the UTC
timestamp to report from and, when needed, replace `customer_id` with the
customer's numeric ID. Leave `customer_id` as `NULL` to report all customers.

- [`customer-financial-flow.sql`](customer-financial-flow.sql) returns every
  immutable wallet transaction for every customer, including balance states,
  campaign/payment/deposit metadata, and external references.
- [`refunded-campaigns.sql`](refunded-campaigns.sql) returns campaign-linked
  refunds with the matching original charge and refund calculation metadata.
- [`campaigns-awaiting-refund.sql`](campaigns-awaiting-refund.sql) identifies
  cancelled/rejected charged campaigns and eligible under-delivered executed
  campaigns that have no completed campaign refund.
