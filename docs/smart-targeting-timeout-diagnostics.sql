-- Run during a stalled sampling job or campaign save. Read-only: this does
-- not cancel jobs or terminate sessions. Capture results before restarting.
-- Query text can contain customer data; redact before sharing.
SELECT activity.pid, activity.application_name, activity.state,
       clock_timestamp() - activity.xact_start AS transaction_age,
       clock_timestamp() - activity.query_start AS query_age,
       activity.wait_event_type, activity.wait_event,
       pg_blocking_pids(activity.pid) AS blocking_pids,
       LEFT(activity.query, 2000) AS query
FROM pg_stat_activity AS activity
WHERE activity.datname = current_database()
  AND activity.pid <> pg_backend_pid()
  AND (activity.state <> 'idle' OR cardinality(pg_blocking_pids(activity.pid)) > 0)
ORDER BY activity.xact_start NULLS LAST;

SELECT waiting.pid AS waiting_pid, waiting.wait_event_type, waiting.wait_event,
       blocker.pid AS blocker_pid, blocker.state AS blocker_state,
       clock_timestamp() - blocker.xact_start AS blocker_transaction_age,
       LEFT(waiting.query, 1000) AS waiting_query,
       LEFT(blocker.query, 1000) AS blocker_query
FROM pg_stat_activity AS waiting
CROSS JOIN LATERAL unnest(pg_blocking_pids(waiting.pid)) AS blocked_by(pid)
JOIN pg_stat_activity AS blocker ON blocker.pid = blocked_by.pid
WHERE waiting.datname = current_database();
