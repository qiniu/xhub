# Monthly personal budget resets

Administrators can choose no automatic reset or a monthly reset on day 1–31 for each user. Resets take effect at midnight Beijing time (UTC+8). If the selected day does not exist in a month, the reset occurs on that month's last day. For example, day 31 resets on February 28 (29 in leap years), then on March 31.

A reset clears the user's spending for the current period while retaining historical usage and bills. Unused balances do not roll over. Organization, team, and key budgets are calculated independently; a personal reset does not remove their restrictions. Soft-budget behavior remains unchanged: requests are admitted while the balance is positive, settled using actual usage, and subsequent requests are rejected once the balance is exhausted.

## Configuration

- User editor: select the budget reset day. User details display the next reset time.
- CSV bulk creation: the optional `budget_reset_day` column accepts an empty value or `0` for no reset, or `1`–`31` for a monthly date. Existing templates remain compatible.
- Bulk editing: set the budget limit and reset day independently. Both default to keeping existing values. You can change either field alone; the entire batch is saved atomically.

```csv
user_email,user_role,password,user_alias,max_budget,budget_reset_day
alice@example.com,user,Replace-before-import-123!,Alice,20,1
bob@example.com,user,Replace-before-import-456!,Bob,20,15
carol@example.com,user,Replace-before-import-789!,Carol,20,0
```

Replace the example accounts and initial passwords before importing. Imports continue to report success or failure per row; bulk editing either succeeds completely or rolls back completely.

## API contract

`POST /user/new` and `POST /user/update` accept an integer `budget_reset_day` from 0 through 31. Omitting it during creation defaults to 0; omitting it during an update preserves the existing value. Nulls, strings, fractions, and out-of-range numbers return 400. Read responses include `budget_reset_day` and `budget_reset_at` (an ISO timestamp or null). The legacy `budget_duration` compatibility field does not configure personal calendar-month resets.

`POST /user/bulk_update` accepts either field independently and requires at least one:

```json
{"users":[{"user_id":"example-user-id","budget_reset_day":15}]}
```

```json
{"all_users":true,"user_updates":{"max_budget":20,"budget_reset_day":1}}
```

`max_budget: null` removes the fixed personal limit; `budget_reset_day: 0` disables periodic resets. These are separate operations. Bulk updates remain restricted to platform administrators and support at most 1,000 users. Single-user reset-day updates use the existing user-budget management permissions.

## Data and runtime behavior

Changing the schedule only arranges the next reset; it does not immediately clear spending. Saving the same date again does not postpone the next reset. Disabling resets preserves the existing period start so that delayed charges do not consume the current personal budget again.

Background maintenance, account reads, and request admission apply overdue resets. After several months of downtime, the schedule advances directly to the current period without accumulating monthly allowances. Resets, audits, configuration changes, and usage settlement share the same database write transaction lock. Concurrent retries create only one `user.budget_reset` audit record. Requests started in the previous period and completed in the next retain their historical bills without consuming the next period's personal budget; other budget levels continue to account for their charges independently.

Upgrades add the necessary columns automatically. Existing users default to no reset and retain their spending. Redis pending charges are filtered by the personal budget period; malformed data does not silently relax the limit. Clients cannot use the date fields to set spending or the next reset timestamp directly.
