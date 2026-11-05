# Conversation history storage

The unified Responses endpoint stores conversation history for continuation. Full snapshots can repeat earlier messages across turns. The compact format stores lossless message blocks within the existing caller, public model and endpoint scope, and stores an ordered block index for each response. Larger blocks are compressed and verified by length and digest. Authentication, budgets, native bypass routing and log retention are unchanged.

New tables `conversation_history_blocks` and `conversation_history_manifests` are created on startup. Both formats are readable. New writes use the old format by default; set `XHUB_COMPACT_HISTORY=1` in the process environment and restart to enable compact writes. Disabling it affects future writes only. A rollback must still use a binary capable of reading compact records. Block and index writes are transactional; corrupted or missing blocks fail continuation, and a failed history write cannot publish a successful Responses terminal event.

## Migrating legacy records

A legacy key is a one-way digest, so its original caller scope cannot safely be reconstructed across different responses. The migration assigns each old record a separate scope, keeps the old copy, and verifies each reconstructed history in the transaction. It does not deduplicate across old records, remove legacy rows, or collect unreferenced blocks; storage can temporarily grow. Newly written responses can share blocks within their authenticated scope.

Confirm that every running gateway understands the compact format and that a complete backup has been restored and verified separately. Run one batch with:

```sh
XHUB_DATABASE_URL="$YOUR_DATABASE_URL" go run ./cmd/history-compact --execute --confirm-no-legacy-writers --limit=50
```

Resume with `--after` using the printed `next` value. Starting again from an empty cursor skips records already migrated. The confirmation flag is an operator assertion, not an automatic check of other instances. Running the migration while an older writer is active can leave its copy stale.

Before release, test JSON and streaming continuation, tool calls, identity isolation, failures, concurrent writes, migration reruns and browser flows with an isolated PostgreSQL database and local provider fixture. Measure storage and latency against representative data. Before removing any old copies, validate references, backup restoration and available disk space; ordinary PostgreSQL vacuum usually makes space reusable rather than immediately returning it to the filesystem.
