package shm

import (
	"errors"
	"fmt"
	"os"
	"sync/atomic"
	"unsafe"

	"github.com/edsrzf/mmap-go"
)

type Order struct {
	// All uint64s first (8-byte aligned)
	OrderID   uint64
	Price     uint64
	Timestamp uint64
	User_id   uint64
	// Then uint32s (4-byte aligned)
	Quantity uint32
	Symbol   uint32
	// Then uint8s (1-byte aligned)
	Side       uint8 // 0=buy, 1=sell
	Status     uint8 // 0=pending, 1=filled, 2=rejected
	Order_type uint8
	// Array of bytes last
	_pad [5]byte
}

type TempOrder struct {
    OrderID    uint64 `json:"order_id"`
    Price      uint64 `json:"price"`
    Timestamp  uint64 `json:"timestamp"`
    Quantity   uint32 `json:"quantity"`
    Symbol     uint32 `json:"symbol"`
    Side       uint8  `json:"side"`
    Order_type uint8  `json:"order_type"`
}

func (o *TempOrder) Validate() error {

	if o.Price == 0 && o.Order_type == 1 {
		// for a limit order, price is required
		return errors.New("price must be > 0 for limit orders")
	}

	if o.Quantity == 0 {
		return errors.New("quantity must be > 0")
	}

	if o.Side != 0 && o.Side != 1 {
		return fmt.Errorf("side must be 0 (buy) or 1 (sell), got %d", o.Side)
	}

	if o.Order_type != 0 && o.Order_type != 1 {
		return fmt.Errorf("order_type must be 0 (market) or 1 (limit), got %d", o.Order_type)
	}

	// Optional: timestamp > 0, or within some window, etc.
	if o.Timestamp == 0 {
		return errors.New("timestamp must be non-zero")
	}

	return nil
}

type QueueHeader struct {
	ProducerHead uint64   // Offset 0 4 byte interger
	_pad1        [56]byte // Padding to cache line
	ConsumerTail uint64   // Offset 64
	_pad2        [56]byte // Padding
	Magic        uint32   // Offset 128
	Capacity     uint32   // Offset 132
}

const (
	QueueMagic    = 0xDEADBEEF
	QueueCapacity = 65536 // !!IMP: match Rust
	OrderSize     = unsafe.Sizeof(Order{})
	HeaderSize    = unsafe.Sizeof(QueueHeader{})
	TotalSize     = HeaderSize + (QueueCapacity * OrderSize)
)

type Queue struct {
	file   *os.File
	mmap   mmap.MMap // this is the array of bytes wich we will use to read and write
	header *QueueHeader
	orders []Order
}

func CreateQueue(filePath string) (*Queue, error) {
	_ = os.Remove(filePath)

	file, err := os.OpenFile(filePath, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o666)
	if err != nil {
		return nil, fmt.Errorf("failed to create file: %w", err)
	}

	// set the size of the file
	if err := file.Truncate(int64(TotalSize)); err != nil {
		file.Close()
		return nil, fmt.Errorf("failed to truncate file: %w", err)
	}

	// sync to disk before mmap
	if err := file.Sync(); err != nil {
		file.Close()
		return nil, fmt.Errorf("failed to sync file: %w", err)
	}
	// m is just a byte array that is mapped to the real file on the Ram
	m, err := mmap.Map(file, mmap.RDWR, 0)
	if err != nil {
		file.Close()
		return nil, fmt.Errorf("failed to mmap: %w", err)
	}

	// try to lock in RAM
	if err := m.Lock(); err != nil {
		// proceed without locking;
		// caller may tune ulimit -l / CAP_IPC_LOCK
	}

	// initialize header
	header := (*QueueHeader)(unsafe.Pointer(&m[0]))
	atomic.StoreUint64(&header.ProducerHead, 0)
	atomic.StoreUint64(&header.ConsumerTail, 0)
	atomic.StoreUint32(&header.Magic, QueueMagic)
	atomic.StoreUint32(&header.Capacity, QueueCapacity)

	// flush to disk
	if err := m.Flush(); err != nil {
		m.Unlock()
		m.Unmap()
		file.Close()
		return nil, fmt.Errorf("failed to flush mmap: %w", err)
	}

	ordersData := m[int(HeaderSize):int(TotalSize)]
	if len(ordersData) == 0 {
		m.Unlock()
		m.Unmap()
		file.Close()
		return nil, fmt.Errorf("orders region empty")
	}
	orders := unsafe.Slice((*Order)(unsafe.Pointer(&ordersData[0])), QueueCapacity)

	return &Queue{
		file:   file,
		mmap:   m,
		header: header,
		orders: orders,
	}, nil

}

// open queue from file on disk and return *Queue mmap-ed
func OpenQueue(filePath string) (*Queue, error) {
	file, err := os.OpenFile(filePath, os.O_RDWR, 0o666)
	if err != nil {
		return nil, fmt.Errorf("failed to open file: %w", err)
	}

	// verify file size
	stat, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, fmt.Errorf("failed to stat file: %w", err)
	}
	if stat.Size() != int64(TotalSize) {
		file.Close()
		return nil, fmt.Errorf("invalid file size: got %d, expected %d", stat.Size(), int64(TotalSize))
	}

	m, err := mmap.Map(file, mmap.RDWR, 0)
	if err != nil {
		file.Close()
		return nil, fmt.Errorf("failed to mmap: %w", err)
	}

	if err := m.Lock(); err != nil {
		// non-fatal; continue without lock
	}

	// validate header
	header := (*QueueHeader)(unsafe.Pointer(&m[0]))
	if atomic.LoadUint32(&header.Magic) != QueueMagic {
		m.Unlock()
		m.Unmap()
		file.Close()
		return nil, fmt.Errorf("invalid queue magic number")
	}
	if atomic.LoadUint32(&header.Capacity) != QueueCapacity {
		m.Unlock()
		m.Unmap()
		file.Close()
		return nil, fmt.Errorf("capacity mismatch: file=%d code=%d", header.Capacity, QueueCapacity)
	}

	ordersData := m[int(HeaderSize):int(TotalSize)]
	if len(ordersData) == 0 {
		m.Unlock()
		m.Unmap()
		file.Close()
		return nil, fmt.Errorf("orders region empty")
	}
	orders := unsafe.Slice((*Order)(unsafe.Pointer(&ordersData[0])), QueueCapacity)

	return &Queue{
		file:   file,
		mmap:   m,
		header: header,
		orders: orders,
	}, nil
}

func (q *Queue) Enqueue(order Order) error {
	consumerTail := atomic.LoadUint64(&q.header.ConsumerTail)
	producerHead := atomic.LoadUint64(&q.header.ProducerHead)

	nextHead := producerHead + 1
	if nextHead-consumerTail > QueueCapacity {
		return fmt.Errorf("queue full - consumer too slow, backpressure at depth %d/%d",
			nextHead-consumerTail, QueueCapacity)
	}

	pos := producerHead % QueueCapacity
	q.orders[pos] = order

	// Publish after write; seq-cst store is sufficient
	atomic.StoreUint64(&q.header.ProducerHead, nextHead)
	return nil
}

func (q *Queue) Dequeue() (*Order, error) {
	producerHead := atomic.LoadUint64(&q.header.ProducerHead)
	consumerTail := atomic.LoadUint64(&q.header.ConsumerTail)

	if consumerTail == producerHead {
		return nil, nil
	}

	pos := consumerTail % QueueCapacity
	order := q.orders[pos]

	// Mark consumed; seq-cst store is sufficient
	atomic.StoreUint64(&q.header.ConsumerTail, consumerTail+1)
	return &order, nil
}

func (q *Queue) Depth() uint64 {
	producerHead := atomic.LoadUint64(&q.header.ProducerHead)
	consumerTail := atomic.LoadUint64(&q.header.ConsumerTail)
	return producerHead - consumerTail
}

func (q *Queue) Capacity() uint64 {
	return QueueCapacity
}

func (q *Queue) Flush() error {
	return q.mmap.Flush()
}

func (q *Queue) Close() error {
	_ = q.mmap.Flush()
	_ = q.mmap.Unlock()
	if err := q.mmap.Unmap(); err != nil {
		_ = q.file.Close()
		return fmt.Errorf("failed to unmap: %w", err)
	}
	return q.file.Close()
}
