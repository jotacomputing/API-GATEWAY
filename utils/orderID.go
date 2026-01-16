package utils

import "sync/atomic"

var nextOrderID uint64

// NextOrderID returns and increments global counter (thread-safe)
func NextOrderID() uint64 {
    return atomic.AddUint64(&nextOrderID, 1)
}
