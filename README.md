# WhatsMeow Basileia

Gateway de API para WhatsApp em **Go**, baseado em [`whatsmeow`](https://github.com/tulir/whatsmeow), com rotas compatíveis com a **UAZAPI/Basileapi**, filas no **RabbitMQ**, estado no **PostgreSQL**, cache/limites/tempo real no **Redis** e mídia no **MinIO**.

> ⚠️ É um cliente **não oficial** do WhatsApp. Sempre existe risco de restrição/banimento do número. Use números dedicados, aqueça números novos, evite disparos frios em massa e ative o *send pacing* (veja abaixo).

---

## Arquitetura

```
             ┌──────────── HTTP (chi) ─────────────┐
 clientes ──▶│ auth (API keys / token da instância) │──▶ /ws, /api/sse (tempo real)
             │ rate limit · CIDR · auditoria        │
             └──────┬───────────────────┬───────────┘
                    │ enfileira job     │ ações síncronas (grupos, chats, ...)
                    ▼                   ▼
   message_jobs (Postgres) ──▶ RabbitMQ ──▶ workers ──▶ whatsmeow ──▶ WhatsApp
                                                │
   eventos do WhatsApp ──▶ manager ──┬──▶ histórico (messages) + mídia (MinIO)
                                     ├──▶ RabbitMQ ──▶ outbox webhook_deliveries ──▶ HTTP assinado
                                     └──▶ hub tempo real (Redis pub/sub) ──▶ WebSocket / SSE
```

| Componente | Papel |
|---|---|
| **PostgreSQL** | Sessões do whatsmeow, instâncias, API keys, auditoria, histórico de mensagens, jobs de envio, outbox de webhooks. Migrations versionadas e embutidas no binário. |
| **RabbitMQ** | Transporte das filas (`send_message_queue`, `send_media_queue`, `webhook_events_queue`, `webhook_delivery_queue`) com filas de *retry* (TTL) e *dead-letter*. |
| **Redis** | Rate limit, contadores do *send pacing* e distribuição de eventos em tempo real entre réplicas. Opcional: sem Redis tudo funciona em memória. |
| **MinIO** | Arquivo de mídia (recebida/enviada), upload de mídia e links temporários. Opcional. |

### Garantias de robustez

- **Nenhum envio se perde**: cada envio vira um *job* no Postgres antes de ir para o RabbitMQ. Falhas temporárias (instância reconectando, erro de upload) são re-tentadas com backoff (15s → 1m → 5m → 15m → 1h) até `MESSAGE_MAX_ATTEMPTS`; o status pode ser consultado em `GET /api/message/job/{id}`.
- **Webhooks com outbox**: eventos são gravados em `webhook_deliveries` e entregues por workers; falhas são re-tentadas com backoff exponencial (10s … 4h) até `WEBHOOK_MAX_ATTEMPTS`, depois ficam como `failed` e podem ser reenviadas (*redrive*). Um reconciliador recupera entregas de workers que caíram. Se o RabbitMQ estiver fora, o evento vai direto para o Postgres.
- **RabbitMQ resiliente**: consumers voltam sozinhos após reconexão, *publisher confirms*, retry com atraso e DLQ (`<fila>.dead`) em vez de loop de requeue.
- **Shutdown gracioso**: para de aceitar requisições, termina o que está em andamento, desconecta os clientes WhatsApp (as sessões continuam válidas).
- **Probes**: `/health` (liveness) e `/ready` (Postgres, RabbitMQ, Redis, MinIO).

---

## Como rodar

```bash
cp .env.example .env        # ajuste senhas e opções
docker compose up -d --build
```

- Painel: `http://localhost:3179` (nginx do frontend)
- API: `http://localhost:8080/api` · Swagger: `http://localhost:8080/docs/index.html`
- Métricas: `http://localhost:8080/metrics` (com `METRICS_TOKEN` ou chave admin)

### Primeira chave de admin

Se `ADMIN_API_KEY` não estiver definida, a primeira chave admin é **gerada no primeiro boot**, impressa **uma única vez** no log e salva em `/app/data/.api-key`:

```bash
docker exec whatsmeow-app cat /app/data/.api-key
```

Use-a para entrar no painel e para criar outras chaves em **API Keys**.

### Desenvolvimento local

```bash
cd backend
export DB_ADDRESS='postgres://whatsmeow:securepassword@localhost:5234/whatsmeow?sslmode=disable'
export RABBITMQ_URL='amqp://whatsmeow:securepassword@localhost:5675/' REDIS_URL='localhost:16379'
export MINIO_ENDPOINT='localhost:19000' MINIO_ACCESS_KEY=whatsmeowAdmin MINIO_SECRET_KEY=securepassword
go run github.com/swaggo/swag/cmd/swag@v1.16.6 init -g cmd/server/main.go --output docs
go run ./cmd/server

cd ../frontend && npm ci && npm run dev   # proxy de /api e /ws para :8080
```

Testes (os de integração só rodam com um banco descartável):

```bash
cd backend
TEST_DATABASE_URL='postgres://user:pass@localhost:5432/wm_test?sslmode=disable' go test -p 1 ./internal/...
```

---

## Autenticação e permissões

Toda rota em `/api` (exceto `/api/health` e `/api/ready`) exige uma credencial, enviada em qualquer um destes lugares:

`Authorization: Bearer <key>` · `X-API-Key` · `api_key` · `token` · `admintoken` · `?api_key=` / `?token=` (para SSE/WebSocket)

| Credencial | Acesso |
|---|---|
| **Chave `admin`** | Tudo, inclusive gerenciar chaves (se não tiver escopo), auditoria, logs, webhooks globais. |
| **Chave `operator`** | Gerenciar instâncias e enviar mensagens. |
| **Chave `viewer`** | Somente leitura (token das instâncias aparece mascarado). |
| **Token da instância** (`api_key` da instância) | Como antes: opera **somente a própria instância**. |

Chaves podem ser limitadas a instâncias específicas (`allowed_instances`). Só o hash SHA-256 é guardado; o texto da chave aparece uma única vez na criação.

**Qual instância?** O token da instância já define. Com uma chave admin/operator, informe `device_id` no corpo (rotas antigas), o header `X-Instance-Id` ou `?instance=`.

Também há rate limit por credencial (`RATE_LIMIT_PER_MINUTE`, headers `X-RateLimit-*`/`Retry-After`), allowlist de IP (`ALLOWED_CIDRS`) e trilha de auditoria (`GET /api/audit`).

---

## Rotas

Todas as rotas existentes **continuam com o mesmo caminho e o mesmo formato**. As respostas de envio ganharam o campo `job_id`.

### Instâncias (REST)
| Método | Rota | |
|---|---|---|
| POST | `/api/device` | criar instância |
| GET | `/api/device` | listar |
| PUT | `/api/device/{id}/rename` · `/webhook` · `/proxy` · `/credentials` | alterar |
| DELETE | `/api/device/{id}` | excluir |
| POST | `/api/device/{id}/reconnect` | reconectar |
| GET | `/api/device/{id}/qr` | QR via SSE |
| GET | `/api/device/{id}/status` | status |
| POST | `/api/device/{id}/pair-code` | **novo** — pareamento por código (`{"phone": "5511..."}`) |
| POST | `/api/device/{id}/logout` · `/disconnect` | **novo** — desvincular / desconectar mantendo a sessão |
| GET/PUT | `/api/device/{id}/settings` | **novo** — rejeitar chamadas, sempre online, leitura automática, ignorar grupos/status nos webhooks, delay entre envios |
| GET | `/api/device/{id}/messages` | **novo** — histórico (`?chat=&limit=&offset=&before=`) |
| GET | `/api/device/{id}/pacing` | **novo** — uso do *send pacing* hoje |

### Envio
`POST /api/message/send` · `/message/media` · `/message/interactive/button` · `/message/interactive/copy` e as rotas UAZAPI `POST /api/send/text|media|contact|location|status|menu|button|poll|carousel|location-button|request-payment|pix-button` e `/api/message/presence`.

Opções aceitas em todas: `delay` (ms de "digitando..."), `replyid` (responder citando), `mentions` (números separados por vírgula ou `all`), `track_source`/`track_id`. Em `/send/media`, `file` aceita URL, base64 ou `media:<key>` (de `/api/media/upload`).

`GET /api/message/job/{id}` mostra se o envio está `queued`, `sending`, `retrying`, `sent` ou `failed` (com o erro).

### Ações em mensagens (UAZAPI)
`POST /api/message/react · delete · edit · markread · pin · download · find`, `GET/DELETE /api/message/async`, `POST /api/instance/updateDelaySettings`, `POST /api/media/upload`.

### Campanhas (UAZAPI `/sender`)
`POST /api/sender/simple` (mesma mensagem para vários números), `/sender/advanced` (mensagem por destinatário), `/sender/edit` (`stop`/`continue`/`delete`), `GET /sender/listfolders`, `POST /sender/listmessages`, `/sender/cleardone`, `DELETE /sender/clearall`. `delayMin`/`delayMax` em segundos; `scheduled_for` em ms epoch ou minutos a partir de agora.

### Instância, perfil, grupos, canais, chats (UAZAPI)
- `GET /api/instance/status`, `POST /instance/connect` (QR em PNG ou código com `phone`), `/instance/disconnect`, `/instance/reset`, `DELETE /instance`, `/instance/updateInstanceName`, `GET/POST /instance/privacy`, `/instance/presence`, `GET/POST /instance/proxy`; admin: `/instance/create`, `/instance/all`
- `/api/profile/name`, `/profile/image`, `/call/reject`
- `/api/group/create|info|inviteInfo|join|leave|list|resetInviteCode|updateAnnounce|updateJoinApproval|updateMemberAddMode|updateDescription|ephemeral|updateImage|updateLocked|updateName|updateParticipants`, `/community/create|editgroups`
- `/api/newsletter/create|list|info|link|follow|unfollow|subscribe|mute|unmute|messages|updates|viewed|reaction`
- `/api/chat/block|blocklist|archive|read|mute|pin|ephemeral|check|details|delete|find|labels`, `/label/edit`, `/labels`, `/contacts`, `/contacts/list`

A lista completa com exemplos está no Swagger (`/docs/index.html`).

---

## Webhooks

Continuam funcionando como antes: `webhook_url` da instância (ou `WEBHOOK_URL` global) recebe **o mesmo corpo** de sempre. Agora também é possível registrar vários webhooks por instância ou globais, com filtro de eventos e de mensagens:

```bash
curl -X POST localhost:8080/api/webhooks -H "token: <token-da-instancia>" -H 'Content-Type: application/json' \
  -d '{"url":"https://n8n.exemplo.com/webhook/x","events":["messages","connection"],"secret":"s3cret"}'
```

(Compatível com UAZAPI: `GET/POST /api/webhook`, `/api/webhook/errors`, `/api/globalwebhook`.)

Eventos: `messages`, `receipts.update`, `presence.update`, `groups.update`, `calls.offer`, `connection.update` (novo: open, close, logged_out, banned, ...). Aliases UAZAPI aceitos (`connection`, `messages_update`, `call`, ...). Filtros `exclude_messages`: `fromMeYes`, `fromMeNo`, `isGroupYes`, `isGroupNo`, `wasSentByApi`, `wasNotSentByApi`.

**Headers de cada entrega:** `X-Webhook-Event`, `X-Webhook-Delivery`, `X-Webhook-Attempt`, `X-Webhook-Timestamp`, `X-Webhook-Instance` e, com segredo, `X-Webhook-Signature`.

**Verificando a assinatura** (Node.js):

```js
const crypto = require('crypto')
const expected = 'sha256=' + crypto.createHmac('sha256', SECRET)
  .update(req.headers['x-webhook-timestamp'] + '.' + rawBody).digest('hex')
const ok = crypto.timingSafeEqual(Buffer.from(expected), Buffer.from(req.headers['x-webhook-signature']))
// rejeite também timestamps com mais de 5 minutos
```

Painel **Webhooks** (ou `GET /api/webhooks/deliveries?status=failed`) mostra falhas e permite reenviar uma ou todas (`POST /api/webhooks/deliveries/redrive`).

---

## Tempo real

```js
const ws = new WebSocket('wss://seu-host/ws?api_key=<key>&events=messages,connection')
ws.onmessage = e => console.log(JSON.parse(e.data)) // {event, instance_id, timestamp, data}
```

Também via SSE: `GET /api/sse?token=<key>&events=messages`. O token da instância só recebe eventos da própria instância. Com Redis, eventos chegam a clientes conectados em qualquer réplica.

---

## Proteção contra banimento (send pacing)

Com `SEND_PACING_ENABLED=true`, cada instância tem:

- **limite diário** que cresce com a idade da sessão (`SEND_PACING_WARMUP_SCHEDULE`, ex.: 50, 100, 200, ... mensagens/dia);
- **limite de conversas novas** por dia (`SEND_PACING_COLD_DAILY_CAP`) — primeiro contato é o que mais gera banimento;
- **disjuntor**: após `SEND_PACING_FAILURE_BREAKER` falhas seguidas, pausa 30 min.

Envios acima do limite **não são perdidos**: o job é reagendado para o dia seguinte. Combine com `SIMULATE_TYPING=true` e o delay por instância (`/api/instance/updateDelaySettings` ou painel).

---

## Migrando da 0.0.7

1. **Faça backup do Postgres** (`pg_dump`). As migrations são aditivas e idempotentes, mas backup é obrigatório antes de qualquer upgrade.
2. Suba a nova versão: as tabelas novas são criadas e as instâncias existentes continuam pareadas.
3. **Autenticação agora é obrigatória** por padrão:
   - integrações que já mandam o **token da instância** (`token`/`api_key`/`x-api-key`) **continuam funcionando sem mudança**;
   - chamadas sem credencial (ex.: `GET /api/device` sem header, ou só `device_id` no corpo) passam a receber `401`: use uma chave (admin/operator);
   - o painel pede login — use a chave de `/app/data/.api-key`;
   - para migrar aos poucos, `AUTH_REQUIRED=false` restaura o comportamento antigo (não recomendado em produção).
4. Defina `WEBHOOK_SECRET` se quiser validar a assinatura dos webhooks.

---

## Variáveis de ambiente

Todas estão documentadas em [`.env.example`](.env.example). Principais: `DB_ADDRESS`, `RABBITMQ_URL`, `REDIS_URL`, `MINIO_*`, `ADMIN_API_KEY`, `AUTH_REQUIRED`, `WEBHOOK_URL`, `WEBHOOK_SECRET`, `MEDIA_ARCHIVE_ENABLED`, `MESSAGE_RETENTION_DAYS`, `SEND_PACING_*`, `RATE_LIMIT_PER_MINUTE`, `ALLOWED_CIDRS`, `METRICS_TOKEN`, `LOG_LEVEL`.

## Estrutura

```
backend/
  cmd/server            ponto de entrada (wiring + shutdown gracioso)
  internal/api          HTTP: rotas, middlewares, handlers (REST + UAZAPI)
  internal/auth         API keys, papéis, escopos
  internal/audit        trilha de auditoria
  internal/ratelimit    rate limit (Redis / memória)
  internal/realtime     hub WebSocket/SSE (Redis pub/sub)
  internal/metrics      Prometheus
  internal/service/messaging   jobs de envio, workers, pacing, campanhas
  internal/service/messages    histórico, status, mídia, chats, etiquetas
  internal/service/webhook     outbox, entrega assinada, reconciliador
  internal/infrastructure      postgres (migrations), rabbitmq, redis, minio, whatsapp (manager)
frontend/               painel React (login, instâncias, mensagens, webhooks, chaves, auditoria)
```
