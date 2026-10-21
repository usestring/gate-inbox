# Timestamp units in state.db

`state.db` stores times as integers, but not in one unit. The older tables use Unix nanoseconds, written by `encodeTime` in `internal/store/store.go`. Tables added later use Unix milliseconds, written with `time.Time.UnixMilli`. Each table is read back in the unit it was written in, so Gate Inbox's own reads are correct. Comparing a column from one group with a column from the other is not: a millisecond value read as nanoseconds lands in January 1970.

| Unit | Table | Columns |
|---|---|---|
| ns | `sessions` | `created_at`, `last_status_at`, `agent_launched_at`, `killed_at`, `archived_at`, `spawner_read_at` |
| ns | `session_inbox` | `sent_at`, `claimed_at`, `delivered_at`, `read_at`, `dropped_at` |
| ns | `session_ends` | `ended_at` |
| ns | `tasks` | `created_at`, `updated_at` |
| ns | `forge_prs` | `updated_at` |
| ms | `forge_prs` | `fetched_at` |
| ms | `forge_tickets` | `fetched_at` |
| ms | `session_prs` | `first_seen` |
| ms | `dialog_answers` | `created_at` |
| ms | `permission_grants` | `created_at`, `revoked_at`, `expires_at` |
| ms | `relay_attestations` | `answered_at`, `created_at`, `spent_at`, `noted_at` |
| ms | `seal_uses` | `used_at` |
| ms | `work_seen` | `seen_at` |

In every column, `0` means "not set". `decodeTime` also reads values below 10¹² as Unix seconds, the unit of rows written before nanosecond precision.

When querying by hand, convert to one unit before comparing across groups. For example, divide nanoseconds by 1,000,000 to get milliseconds:

```sql
SELECT a.id, s.name
FROM dialog_answers a JOIN sessions s ON s.id = a.target_session
WHERE a.created_at > s.created_at / 1000000;
```

A new table should use `encodeTime` and `decodeTime`, so that it joins the nanosecond group. Moving the millisecond tables onto nanoseconds would need a data migration plus a matching change to every reader in the same release. Until that happens, this page is the record of which unit each column holds.
