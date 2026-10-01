# Idempotency hardening proposal / 幂等性增强方案

Status: follow-on proposal, updated against current main. Milestone 8 already
implements optional enqueue keys with memory/Redis claim-or-reuse storage and CLI
support. Keys currently have no expiry or scope beyond the key string; changed
payloads reuse the existing ID. The stronger semantics below are proposed changes,
not the current API contract or unfinished milestone 8 acceptance requirements.

状态：后续增强方案。里程碑 8 已实现内存及 Redis 提交键去重和 CLI 支持。当前键无
过期时间或额外作用域，负载变化仍复用原 ID。下文的更强语义属于新提案，不代表
当前 API 契约，也不意味着已完成的里程碑 8 尚未交付。

## Outcome / 预期行为

Repeated submissions of the same logical request should return the same task ID
within a configured retention window. Workers should reject stale ownership and
avoid repeating an execution whose completion has already been recorded.
This does not guarantee exactly-once external side effects: a handler can update
an external system and crash before recording completion. Handlers must use the
business operation's stable key or a transaction in the destination system.

同一逻辑请求在约定的保留窗口内重复提交，应返回同一个任务 ID。工作进程应拒绝
过期的执行所有权，并避免再次执行已记录完成的任务。这不保证外部副作用恰好发生
一次：处理函数可能已完成外部操作，却在记录结果前崩溃。业务仍需使用稳定的操作
标识，或在目标系统中通过事务保证幂等。

## Current gaps / 当前缺口

- Unkeyed submissions generate new IDs; keyed submissions already reuse an ID.
  The idempotency claim and broker publication remain separate operations.
- `enqueueMessage` publishes before writing PENDING; a fast worker's result can
  be overwritten by that late write. Several result-write errors are ignored.
- Redis reservations are identified by serialized messages, not unique delivery
  receipts. Duplicate messages can share a lease entry, and a stale worker can
  acknowledge a newer reservation.
- Reservations expire after 30 seconds without renewal. A long-running handler
  can overlap with a recovered delivery. Worker capacity is acquired after dequeue.
- Retry publication and acknowledgment are separate operations; a crash between
  them can leave both the original delivery and its retry eligible for execution.
- Result and DLQ writes are separate from acknowledgment; persistence failures
  must not silently become acknowledged completion.

当前需要同时处理重复提交、结果状态回退、预留身份不唯一、租约缺少续期，以及
重试和结果保存过程中的崩溃窗口。单独增加一个 Redis 锁不能覆盖这些问题。

## Proposed public contract / 建议的公开契约

```go
id, err := app.Enqueue(ctx, "send_email", payload,
    taskforge.WithIdempotencyKey("welcome:user-42"),
)
```

The option and CLI `-idempotency-key` flag already exist. Preserve the existing
`(string, error)` return type; the table below proposes stronger semantics.

上述选项和 CLI 参数已实现。保留现有返回类型；下表提出更强的行为契约。

| Case / 情况 | Proposed behavior / 建议行为 |
|---|---|
| No key / 无键 | Existing submission behavior; each call creates a new task / 每次创建新任务 |
| Same scope, key and request / 同范围、同键、同请求 | Return original ID; do not enqueue again / 返回原 ID，不再次入队 |
| Same scope/key, changed request / 同键但请求不同 | Typed conflict error; no new task / 返回可识别的冲突错误，不创建任务 |
| Retry / 自动重试 | Same task ID and business key; advance attempt once / 保持 ID 和业务键，尝试次数仅推进一次 |
| Retained SUCCESS or FAILED / 保留期内已成功或失败 | Return original ID; do not silently rerun / 返回原 ID，不隐式重跑 |
| Expired terminal record / 终态记录过期 | A fresh submission may create a new ID / 再次提交可创建新任务 |
| Explicit DLQ replay / 显式死信重放 | New task ID; clear submission key by default; allow a new explicit key / 新 ID，默认清除提交键，可显式指定新键 |
| Periodic schedules / 周期调度 | Separate logical request per occurrence / 每次触发是独立请求 |

Scope: configured namespace + queue + task name + caller key. Default namespace
`taskforge`; callers must include tenant identity in the key when needed.
Use an unambiguous encoded tuple and hash for storage keys, not raw concatenation.
Fingerprint the serialized JSON payload, normalized timeout, priority, retry
policy and requested scheduling options. Document byte-level JSON comparison;
semantically equivalent but differently serialized custom JSON may conflict.
Compare an explicit delay duration, not the wall-clock timestamp derived from it.
Generated task IDs, enqueue times and attempts are excluded.

键的作用域包含命名空间、队列和任务名称。多租户调用方需纳入租户标识。请求指纹
覆盖负载及影响执行的选项；相对延迟按提交的时长比较，避免重复请求因当前时间不同
而冲突。JSON 采用序列化字节比较，不承诺语义等价 JSON 自动归一化。

Retention proposal: keep active records until terminal completion; retain terminal
records for 24 hours by default. Duplicate requests do not extend that deadline.
Do not let a delayed or retrying task's key expire while its work is outstanding.
Result retention must cover terminal deduplication retention (or be unlimited);
reject incompatible settings when this feature is enabled. Active records may
require future orphan inspection tooling; do not silently expire them to save space.

建议活动任务的键持续保留，终态后默认保留 24 小时，重复请求不延长窗口。结果保留
期限不得短于终态去重期限；延迟和重试中的任务不能因固定 TTL 到期而失去保护。

## Implementation stages / 实施阶段

### A. Atomic submission deduplication / 原子提交去重

Implement memory and standalone Redis support behind a narrow broker capability
such as `EnqueueUnique`, returning the chosen ID and whether it was newly accepted.
Keep shared identity types in `internal/task`; migrate the existing separate
`internal/idempotency` claim store into a boundary that can commit with enqueue.

- Redis: one validated Lua operation checks the key/fingerprint and creates both
  the identity record and ready/delayed message. Validate inputs and key types
  before writes; script errors do not imply rollback. On an uncertain network
  response, the caller retries with the same key; never blindly delete the record.
- Memory: coordinate identity and queue insertion under one ownership boundary.
  Cancellation or full queues must not strand a successful-looking key. Delayed
  work must be owned by the broker lifetime, not the enqueue caller's context.
- Initialize PENDING with create-if-absent semantics; duplicate submissions must
  never overwrite a later state. Surface persistence failures and define recovery.
- Strong Redis mode initially requires broker/result/DLQ to use the same Redis
  endpoint, DB and namespace; reject mixed configurations for that mode.
- Redis Cluster and cross-store transactions are outside the initial scope.

先完成“同一键只接收一次”的原子操作，并支持内存和单节点 Redis。去重记录与队列
必须一起提交；网络结果不确定时通过同一键重试恢复。此阶段只交付提交去重，不能
单独宣称本增强方案全部完成。

### B. Delivery ownership and renewal / 交付所有权与续期

Introduce a delivery envelope containing the task message, unique receipt token,
and monotonic ownership generation. Ack, renewal, recovery and finalization must
check current ownership. Do not use serialized task payloads as reservation IDs.

- Acquire worker capacity before reserving work.
- Renew live reservations periodically (proposed interval: at most one third of
  the lease). Use Redis time for distributed lease decisions.
- Lease loss cancels handler context and forbids that worker from committing
  Taskforge state, publishing retries, or acknowledging a replacement delivery.
- An expired worker can still run external code if it ignores cancellation;
  fencing only protects stores that enforce the ownership token.
- Keep a task-level execution record so different receipts for the same task
  cannot independently advance its attempt or commit completion.

使用唯一交付凭据及所有权代次，避免旧 Worker 确认新 Worker 的消息。增加续期，先
获取并发额度再领取任务。丢失所有权后取消 context，并拒绝过期 Worker 的内部状态
写入；无法强制终止不响应取消的业务代码。

### C. Recoverable completion, retries and replay / 可恢复的完成、重试与重放

Create a storage coordination boundary for owner-checked transitions, rather than
letting Worker independently write results and acknowledge messages.
For the supported same-Redis configuration, commit terminal result, execution
marker, applicable DLQ record, identity retention and receipt removal together.
Atomically commit RETRYING, the next attempt/message and removal of the old receipt.
Validate all records before mutation. Memory implements corresponding transitions
under shared synchronization, without claiming restart persistence.

Persisted completion allows a redelivered completed task to be acknowledged without
calling its handler. A stale attempt cannot overwrite a newer attempt. Store errors
leave work recoverable and are surfaced; do not acknowledge unrecorded completion.
Execution markers must outlive any associated live delivery/retry; terminal cleanup
must not leave stale queue entries eligible after marker expiry. Deleting a result
or purging a DLQ entry must not remove submission identity or execution protection.

将结果、完成标记、死信记录及确认放入检查所有权的统一状态转换中；重试也需原子
推进。持久化失败不能静默确认消息。保留执行标记直到相关交付均已清理，避免过期后
旧消息重新执行。死信清除与结果清除不能隐式取消去重保护。

### D. Documentation and release gate / 文档与交付检查

Document the API/CLI in English and Chinese, retention/conflict semantics, handler
responsibilities, supported backend combinations and the lack of exactly-once side
effect guarantees. Update README, ARCHITECTURE and MILESTONES after verified delivery.
Retain existing no-key examples and migration guidance for old Redis queue data;
version the receipt/key schema and require draining old workers before migration.

补充双语使用说明及旧队列数据迁移步骤。只有经过验证的能力才能标为完成；新旧
Worker 不应在不兼容的预留格式下混跑。

## Acceptance tests / 验收测试

1. Concurrent producers in separate processes submit one key: all return one ID,
   one logical job is queued; changed payload/options cause a conflict.
2. Same key in another namespace, queue or task name stays independent; no-key
   submissions continue to create independent jobs.
3. A lost enqueue response followed by resubmission returns the accepted ID; a
   failed enqueue never leaves a key pointing to work that cannot be recovered.
4. Delayed tasks and retries retain identity; terminal expiry permits a new task;
   duplicates cannot reset RUNNING/SUCCESS/FAILED to PENDING.
5. A handler running longer than the initial lease remains owned with renewal;
   multiple healthy workers do not concurrently start it under valid ownership.
6. Worker death permits recovery; an old receipt cannot ack, renew, commit a result
   or publish a retry after ownership has changed.
7. Crash/failure injection around retry and finalization cannot create two valid
   next attempts or acknowledge missing results/DLQ records.
8. Redelivery after recorded completion skips the handler. A crash after an external
   side effect but before completion explicitly demonstrates the documented limit.
9. DLQ replay allocates a fresh ID, preserves the original record and follows the
   new-key policy; purging DLQ does not reopen an old submission key.
10. Unit tests use controlled clocks for expiry and ownership; real Redis tests use
    isolated disposable data, separate producer/worker processes and injected failure
    points. Run the full suite and focused concurrency tests with the race detector.

验收重点是并发重复提交、崩溃恢复、长任务续期、过期所有权拒绝、重试只推进一次，
以及明确展示外部副作用的保证边界。跨进程验证应使用真实独立进程，而不只是同一
进程中的多个 App 实例。

## Review checkpoints / 设计检查点

Before implementing stage A, finalize fingerprint encoding, namespace/config validation,
and the active-record retention mechanism that stage C will complete. Before stages B/C,
write the storage transition interfaces and crash-state table: every interrupted
operation must have an explicit recovery path. These are implementation review
checkpoints, not additional user-approval gates.

实施前明确编码、配置和状态转换；对每个中断点列出恢复路径。先交付 阶段 A，再完成
阶段 B 和 阶段 C，最后按 阶段 D 验收整个里程碑。
