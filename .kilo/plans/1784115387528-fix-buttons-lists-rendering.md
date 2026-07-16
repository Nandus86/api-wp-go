# Fix Buttons and Lists Not Rendering on WhatsApp — Implementation Plan

## Summary

After exhaustive comparison, the protobuf structure, `biz`/`bot` extra nodes, and routing logic in `whatsmeow-basileia` are structurally **identical** to `evolution-go`. Both systems should produce the same XMPP stanza. The issue requires runtime debugging to identify the exact difference.

## Tasks

### 1. Add comprehensive debug logging to `builder.go` Send() method

File: `backend/internal/service/messaging/builder.go:456-480`

Add logging BEFORE calling `client.SendMessage`:
- Dump the full protobuf message as JSON (using protojson)  
- Dump the `extraNodes` as indented JSON
- Log the JID being sent to
- Log whether the message has `MessageContextInfo.MessageSecret` set

Add logging AFTER `client.SendMessage`:
- Log the response ID on success
- Log the full error on failure

### 2. Add pre-build logging to `worker.go` handleSendMessage()

File: `backend/internal/service/messaging/worker.go:108-225`

Before the builder is used, log:
- The raw JSON payload received from RabbitMQ
- The parsed `SendMessagePayload` as JSON
- Which builder method is about to be called (text/button/list/poll/etc)

### 3. Add a test endpoint to send fixed payloads that match evolution-go exactly

File: `backend/internal/api/bapi.go`

Add a route `/api/debug/send-button` and `/api/debug/send-list` that constructs protobuf messages exactly as evolution-go does (hardcoded structures, not through the builder), then sends them directly. This isolates whether the issue is in the builder or in the library/sender.

### 4. Verify the XMPP stanza is sent correctly

After running the debug endpoint:
- Check WhatsApp app to see if the button/list appears
- Check logs to compare the protobuf payload with what evolution-go generates
- If the debug endpoint works but the normal route doesn't, the issue is in request parsing/worker routing
- If both fail, the issue is in the whatsmeow library or server-side rejection

### Implementation Order

1. Add debug logging to builder.go
2. Add debug logging to worker.go
3. Add debug test endpoints in bapi.go
4. Run test and analyze output
