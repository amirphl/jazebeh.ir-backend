# Bundle message audience uniqueness checks

These PostgreSQL/psql queries find phone numbers that appear more than once in
the sent-message history of one bundle. They include SMS, Bale, Rubika, and
Splus records and include retained historical `processed_campaigns` rows, so a
retry cannot hide a duplicate.

Set the bundle ID at the top of a query before running it:

```sql
\set bundle_id 123
```

Run `01_duplicate_phone_summary.sql` first. A bundle has no duplicate sent
audience when it returns zero rows. Use `02_duplicate_phone_details.sql` to
inspect each offending message record.

Phone numbers are compared after trimming whitespace and removing formatting
characters other than ASCII digits. This catches values such as `0912-123-4567`
and `09121234567` as the same phone number. Empty phone numbers are excluded;
the application uses those for non-delivery audit records.

The queries intentionally do not filter by delivery status: a number has been
used once a sent-message record exists, whether its provider result is pending,
successful, or unsuccessful.
