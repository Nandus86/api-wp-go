# Changelog

## 0.0.8

Robustness release, inspired by OpenWA, keeping Go + whatsmeow + RabbitMQ + Postgres and
turning Redis and MinIO on. Every existing route keeps its path and payload.

### Breaking

- **Authentication is required by default** (`AUTH_REQUIRED=true`). Instance tokens keep
  working for their own instance; anything else needs an API key. The first admin key is
  generated on boot (`/app/data/.api-key`) unless `ADMIN_API_KEY` is set. The dashboard
  now asks for a key. `AUTH_REQUIRED=false` restores the old open behaviour.
- `send_message_queue` messages without a matching instance are rejected at the API
  (`404 instance not found`) instead of being silently dropped by the worker.

### Added

- Postgres migrations (versioned, embedded, advisory-locked) on a shared pool.
- API keys with roles (admin/operator/viewer), per-instance scope, expiry, revocation;
  audit log; per-credential rate limit (Redis); CIDR allowlist; CORS; body size limit.
- Tracked send jobs with retries/backoff, `GET /api/message/job/{id}`, campaigns
  (`/api/sender/*`), async queue listing/cancel, per-instance send delay.
- Message history with delivery status, media archive in MinIO, `/api/media/upload`,
  message actions (react, edit, delete, markread, pin, download, find).
- Send pacing (daily cap by session age, cold-contact cap, failure breaker), typing
  simulation, `replyid`/`mentions`/`delay`/`track_*` options.
- Webhook outbox with HMAC signatures, exponential backoff, failed list, redrive,
  multiple webhooks per instance or global with event/message filters, test endpoint,
  `connection.update` event.
- UAZAPI routes for instance, profile, groups, communities, channels, chats, contacts,
  labels, calls, webhooks and global webhooks.
- Realtime WebSocket (`/ws`, `/api/ws`) and SSE (`/api/sse`) with Redis fan-out.
- Prometheus `/metrics`, `/health` and `/ready` probes, graceful shutdown.
- Pairing by phone code, logout/disconnect, per-instance settings (reject calls,
  always online, auto read, ignore groups/status in webhooks).
- Dashboard: login, API keys, webhooks & deliveries, audit, messages, real settings.
- CI (vet, unit + Postgres integration tests, frontend lint/build, Docker builds).

### Fixed

- RabbitMQ consumers did not come back after a reconnect.
- A failing send was requeued in a tight loop forever (now retry queue + DLQ / job retries).
- Unpaired instances could not get a QR code after a restart (no client was created).
- Reopening the QR stream while a login attempt was running hung.
- Sending to group JIDs built an invalid `...@g.us@s.whatsapp.net` address.
- `/send/media` ignored `text`/`docName`, base64 `file` and sent `ptt` as an image;
  `/send/menu` dropped `footerText`; `/api/message/presence` was documented but not routed.
- The instance table was queried on every WhatsApp event (now cached in memory).
- Dev proxy stripped the `/api` prefix the backend expects.
