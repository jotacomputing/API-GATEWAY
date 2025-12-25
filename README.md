# exch-ws — Frontend integration guide

Quick reference for frontend engineers to integrate with the websocket/HTTP server.

## Purpose
This service exposes a WebSocket endpoint for realtime market/order updates and a small set of HTTP endpoints for health/info. Use the WS for subscriptions, order/cancel actions and receiving order/holding events.

## Start (dev)
Build & run:
```sh
go run ./...
```
(Confirm actual listen address in main.go / Hub/router.go / ws/server.go.)

## Endpoints (frontend-facing)

- WebSocket (primary)
  - Path: /ws  (check Hub/router.go for exact path)
  - Protocol: JSON messages both directions
  - Connect example:
    const ws = new WebSocket("ws://localhost:8080/ws");

- HTTP (common)
  - GET /health — server health (simple 200)
  - (There may be other admin/diagnostic routes — check Hub/router.go)

## WebSocket message contracts

Client -> Server

- Subscribe to a symbol
  {
    "type": "subscribe",
    "symbol": "BTC-USD"
  }

- Unsubscribe
  {
    "type": "unsubscribe",
    "symbol": "BTC-USD"
  }

- Place an order (example)
  {
    "type": "place_order",
    "order": {
      "side": "buy",
      "symbol": "BTC-USD",
      "price": "30000",
      "quantity": "0.1",
      "client_id": "optional-client-id"
    }
  }

- Cancel an order
  {
    "type": "cancel_order",
    "order_id": "12345",
    "symbol": "BTC-USD"
  }

- Query balances / holdings
  {
    "type": "query_holdings",
    "account_id": "user-123"
  }

Server -> Client (events)

- order_event (result of fills/cancels/exec)
  {
    "type": "order_event",
    "event": {
      "order_id": "12345",
      "symbol": "BTC-USD",
      "status": "filled|partial|cancelled|accepted",
      "filled_qty": "0.05",
      "remaining_qty": "0.05",
      "price": "30000",
      "timestamp": 1670000000
    }
  }

- holdings_response (reply to query_holdings)
  {
    "type": "holdings_response",
    "account_id": "user-123",
    "holdings": [
      {"symbol":"BTC-USD","available":"0.5","locked":"0.1"}
    ]
  }

- broadcast (market data / pubsub messages for a subscribed symbol)
  {
    "type": "broadcast",
    "symbol": "BTC-USD",
    "payload": { /* market/orderbook/event payload */ }
  }

- ack / error (generic)
  { "type":"ack","id":"msg-id" }
  { "type":"error","error":"reason","orig_type":"place_order" }

## Best practices & tips
- Open a single WS connection and subscribe to multiple symbols rather than many connections.
- Re-subscribe after reconnect; keep a small client-side subscription cache.
- Use client-generated ids (client_id) to match order acks with UI state.
- Treat broadcast payloads as authoritative for subscribed symbols.

## Where to check server-side details
- Hub/router.go and ws/server.go — real routes and WS handshake
- SymbolManager/ and PubSubManager/ — subscription & broadcast behavior
- shm/ — shapes and names of order/holding events (useful if you need exact field names)

## Example JS usage (minimal)
```js
const ws = new WebSocket("ws://localhost:8080/ws");
ws.onopen = () => ws.send(JSON.stringify({type:"subscribe", symbol:"BTC-USD"}));
ws.onmessage = (m) => {
  const msg = JSON.parse(m.data);
  if (msg.type === "order_event") { /* update order UI */ }
  if (msg.type === "broadcast") { /* update market view */ }
};
```

If you need exact field names for events, I can extract them from the shm/types and hub code and update this file.