# Bundle sent-message duplicate attribution

These PostgreSQL/psql queries classify duplicate sent phone numbers by the
targeting method of the campaigns that sent them. They do not use the contents
of an Excel file: the system does not retain a per-sent-message reference to
the uploaded row, so SQL cannot prove that a particular duplicate originated
inside a file.

They can establish the strongest available database evidence:

- `02_duplicates_involving_non_excel_campaigns.sql` returns zero rows only if
  every sent-message record for every duplicate phone belongs to an
  Excel-targeted campaign.
- A duplicate limited to one campaign can still be a retry or multiple send
  attempts; campaign targeting alone does not establish a duplicate Excel row.
- A duplicate shared by several Excel-targeted campaigns shows reuse across
  Excel-targeted campaigns, but not which input file supplied the number.

The targeting method follows the application rule: a valid explicit
`audience_targeting_method` wins; otherwise a nonempty
`target_audience_excel_file_uuid` means `excel`; otherwise it is `standard`.

Set the bundle ID before running a query:

```sql
\set bundle_id 347
```
