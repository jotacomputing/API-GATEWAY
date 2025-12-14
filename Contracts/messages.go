package contracts

import (
	"encoding/json"
)
type Method string
const (
	SUBSCRIBE 	Method = "SUBSCRIBE"
	UNSUBSCRIBE Method = "UNSUBSCRIBE"
)

type MessageFromUser struct {
	Method Method `json:"method"`
    Params []string `json:"params"`
    ID     int      `json:"id"`
}

type MessageFromPubSubForUser struct {
	Stream string          `json:"stream"`
    Data   json.RawMessage `json:"data"` //  raw for routing, then unmarshal specific type
}

type DepthData struct {
    Event     string     `json:"e"`           // "depth"
    Symbol    string     `json:"s"`         
    EventTime int64      `json:"E"`           // Millisecond timestamp
    TradeTime int64      `json:"T"`           // Transaction time
    FirstID   int64      `json:"U"`           // First update ID
    LastID    int64      `json:"u"`           // Last update ID
    Bids      [][]string `json:"b"`           // [["price", "qty"], ...]
    Asks      [][]string `json:"a"`           // [["price", "qty"], ...]
}

type BookTickerData struct {
    Event       string `json:"e"`  // "bookTicker"
    Symbol      string `json:"s"` 
    EventTime   int64  `json:"E"`
    TradeTime   int64  `json:"T"`
    BestBid     string `json:"b"`  // Best bid price
    BestBidQty  string `json:"B"`  // Best bid quantity
    BestAsk     string `json:"a"`  // Best ask price
    BestAskQty  string `json:"A"`  // Best ask quantity
    UpdateID    int64  `json:"u"`
}

type TradeData struct {
    Event        string `json:"e"`  // "trade"
    Symbol       string `json:"s"` 
    EventTime    int64  `json:"E"`
    TradeTime    int64  `json:"T"`
    TradeID      int64  `json:"t"`  // Unique trade ID
    Price        string `json:"p"`  // Execution price
    Quantity     string `json:"q"`  // Quantity traded
    BuyerOrderID string `json:"a"`  // Buyer order ID
    SellerOrderID string `json:"b"` // Seller order ID
    IsBuyerMaker bool   `json:"m"`  // true = buyer placed limit order
}

type TickerData struct {
    Event     string `json:"e"`  // "ticker" 
    Symbol    string `json:"s"`  
    EventTime int64  `json:"E"`
    Price     string `json:"p"`  // Last traded price 
    // can add volume and other fields 
}