# Fork Source Integration

## Scope / Trigger

Use when divergent sources change billing, localized DTOs, generated Ent code or published migrations.

## Signatures

- `GatewayService.QuoteUsageCost(ctx, *RecordUsageInput)` must match actual `RecordUsage` charges for identical inputs.
- `(*AdminUsageLog).Localized(language)` returns `*AdminUsageLog`, preserving observer fields.
- `schema_migrations(filename, checksum)` keys by full filename and hashes trimmed SQL text; ordering is lexicographic.
- `stripExcelBPSInvalidEncryptedContent(body []byte, invalid map[string]struct{}) ([]byte, []string)` returns the cleaned BPS body and digests to remember.
- `(*openAICodexHTTPDeliveryWriter).FlushError() error` propagates the underlying transport flush failure.

## Contracts

Preserve model-policy snapshots, account group multipliers, peak factors, groupless explicit pricing and Kiro/Adobe options in both quotes and charges. Owner key endpoints retain `APIKeyWithSecretFromService(...).Localized(...)`; other nested/admin DTOs keep masked keys. Localized plaza output retains denied-model filtering and user rates. Append `Vary: Accept-Language` alongside CORS variation.

The user approved the ranxi GPT-6.1 Sol policy on 2026-10-15: API Key accounts, OAuth accounts and SetupToken accounts are exempt from the official long-context surcharge within the inclusive 500k context boundary. Above 500k, or when the upstream model maps away from GPT-6.1 Sol, the exemption does not apply. Administrator-configured pricing intervals still apply. Do not silently restore the former OAuth-only exemption.

Compare every published SQL blob with the deployed baseline. Regenerate Ent and Wire from combined definitions before running compilers: intermediate generator output may be temporarily invalid. Optional regional funds code remains disabled unless a separate cutover is authorized and verified.

BPS invalid-encrypted-content recovery must keep its own stripping and cross-request digest memory in the `bps:` session namespace. Native OpenAI compaction preservation must not change that BPS contract. For HTTP delivery, call `FlushGatewayResponse` on the inner writer and record header delivery only after a successful flush; a wrapper must not hide a transport error.

The 2026-10-30 release crosses the durable billing protocol boundary described in `docs/billing-audit-recovery.md`. Stop admission, drain requests and workers, obtain a clean old-process exit, and then take the database/data/config backup before starting the candidate. Never overlap old and new financial writers or automatically restore the old image after candidate launch. Verify exact image identity, migration ledger, packaged assets and unrelated-container invariants before reopening admission.

`coderOpenAIWSClientConn.WriteJSON` must preserve `json.Encoder` HTML escaping and its trailing newline when timezone binding is present. After marshaling ordinary values, send `append(payload, '\n')` through `WriteFrame` so timezone rewriting still applies. Prepared JSON and raw frames retain their existing framing.

## Validation / Error Matrix

| Condition | Response |
| --- | --- |
| Published SQL changed or production checksum incompatible | Stop release and investigate; never rewrite the ledger |
| Quote differs from charge | Fix before release; cover free and non-unit account rates |
| Localization drops observer fields or denied-model filtering | Preserve wrapper return type and pass deny lists through localization |
| Billing conflict changes customer charges | Obtain an explicit policy decision |
| BPS rejects encrypted content already seen in the session | Strip the BPS-incompatible item and remember its digest without changing native compaction preservation |
| Transport flush fails | Return the failure and do not record successful header delivery |
| Old financial writers have not exited cleanly | Keep admission closed; do not start the candidate |

## Good / Base / Bad Cases

Good: test compositions of both feature sets. Base: a shared numeric migration prefix with distinct full filenames is allowed. Bad: overwrite the migrations directory or replace a whole billing block with one source.

## Tests Required

Assert quote/charge parity, explicit groupless pricing, localization with denied models and observer fields, source ancestry and old SQL preservation. Upgrade a disposable database twice from the exact deployed baseline; preserve checksums, balances, roles, platform data and concurrency settings. Verify historical metadata backfills without inferring unknown groups.

Run `TestCodexWSIntegrationEntrypointWireProfile` and `TestRequestTimezoneWebSocketPreservesJSONAndRawFraming`: disabled/manual modes preserve Encoder bytes; auto mode rewrites timezone and keeps the newline; raw frames gain no newline. Regional persistence tests require Linux directory fsync and POSIX permissions; run them on ext4 rather than weakening those safety checks for Windows.

Cover BPS encrypted-content memory and native compaction protection separately, and verify terminal SSE flush failures retain their delivery provenance. Legacy 292-character ticket fixtures must set `TargetLength: 292` explicitly; the approved runtime default is 780 with fail-closed behavior retained. A slow host filesystem must not be accommodated by deleting fsyncs or widening production timeouts. Record any volatile unit-fixture filesystem separately from ext4-backed persistence and database integration evidence.

## Wrong vs Correct

Wrong: return an embedded `UsageLog.Localized` result for an observer endpoint, discarding admin fields. Correct: return the same `AdminUsageLog` wrapper after localizing its nested groups.

Wrong: route BPS recovery through a native helper that now preserves encrypted compaction items. Correct: use the BPS-specific stripper and preserve the native protection independently.
