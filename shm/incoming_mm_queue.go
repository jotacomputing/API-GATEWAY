package shm

import (
	"errors"
	"fmt"
	"os"
	"sync/atomic"
	"unsafe"

	"github.com/edsrzf/mmap-go"
)

type MMOrder struct {
	OrderID   uint64 // 0 for incoming orders // different for cancel orders
	ClientID  uint64
	Price     uint64
	Timestamp uint64
	//UserId  will always be 0 for market making orders
	Quantity uint32
	Symbol   uint32
	Side       uint8 // 0=buy, 1=sell
	Order_type uint8 // 0 -> post order , 1->cancel order 
	Status     uint8 // 0=pending, 1=filled, 2=rejected
}


func (o *MMOrder) Validate() error {

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

type MMQueueHeader struct {
	ProducerHead uint64   // Offset 0 4 byte interger
	_pad1        [56]byte // Padding to cache line
	ConsumerTail uint64   // Offset 64
	_pad2        [56]byte // Padding
	Magic        uint32   // Offset 128
	Capacity     uint32   // Offset 132
}

const (
	MMQueueMagic    = 0xEAAAAAA2
	MMQueueCapacity = 65536 // !!IMP: match Rust
	MMOrderSize     = unsafe.Sizeof(MMOrder{})
	MMHeaderSize    = unsafe.Sizeof(MMQueueHeader{})
	MMTotalSize     = MMHeaderSize + (MMQueueCapacity * MMOrderSize)
)

type MMQueue struct {
	file   *os.File
	mmap   mmap.MMap // this is the array of bytes wich we will use to read and write
	header *MMQueueHeader
	orders []MMOrder
}

func CreateMMQueue(filePath string) (*MMQueue, error) {
	_ = os.Remove(filePath)

	file, err := os.OpenFile(filePath, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o666)
	if err != nil {
		return nil, fmt.Errorf("failed to create file: %w", err)
	}

	// set the size of the file
	if err := file.Truncate(int64(MMTotalSize)); err != nil {
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
	header := (*MMQueueHeader)(unsafe.Pointer(&m[0]))
	atomic.StoreUint64(&header.ProducerHead, 0)
	atomic.StoreUint64(&header.ConsumerTail, 0)
	atomic.StoreUint32(&header.Magic, MMQueueMagic)
	atomic.StoreUint32(&header.Capacity, MMQueueCapacity)
	// flush to disk
	if err := m.Flush(); err != nil {
		m.Unlock()
		m.Unmap()
		file.Close()
		return nil, fmt.Errorf("failed to flush mmap: %w", err)
	}

	ordersData := m[int(MMHeaderSize):int(MMTotalSize)]
	if len(ordersData) == 0 {
		m.Unlock()
		m.Unmap()
		file.Close()
		return nil, fmt.Errorf("orders region empty")
	}
	orders := unsafe.Slice((*MMOrder)(unsafe.Pointer(&ordersData[0])), MMQueueCapacity)

	return &MMQueue{
		file:   file,
		mmap:   m,
		header: header,
		orders: orders,
	}, nil

}

// open queue from file on disk and return *MMQueue mmap-ed
func OpenMMQueue(filePath string) (*MMQueue, error) {
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
	if stat.Size() != int64(MMTotalSize) {
		file.Close()
		return nil, fmt.Errorf("invalid file size: got %d, expected %d", stat.Size(), int64(MMTotalSize))
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
	header := (*MMQueueHeader)(unsafe.Pointer(&m[0]))
	if atomic.LoadUint32(&header.Magic) != MMQueueMagic {
		m.Unlock()
		m.Unmap()
		file.Close()
		return nil, fmt.Errorf("invalid queue magic number")
	}
	if atomic.LoadUint32(&header.Capacity) != MMQueueCapacity {
		m.Unlock()
		m.Unmap()
		file.Close()
		return nil, fmt.Errorf("capacity mismatch: file=%d code=%d", header.Capacity, MMQueueCapacity)
	}

	ordersData := m[int(MMHeaderSize):int(MMTotalSize)]
	if len(ordersData) == 0 {
		m.Unlock()
		m.Unmap()
		file.Close()
		return nil, fmt.Errorf("orders region empty")
	}
	orders := unsafe.Slice((*MMOrder)(unsafe.Pointer(&ordersData[0])), MMQueueCapacity)

	return &MMQueue{
		file:   file,
		mmap:   m,
		header: header,
		orders: orders,
	}, nil
}

func (q *MMQueue) Enqueue(order MMOrder) error {
	consumerTail := atomic.LoadUint64(&q.header.ConsumerTail)
	producerHead := atomic.LoadUint64(&q.header.ProducerHead)

	nextHead := producerHead + 1
	if nextHead-consumerTail > MMQueueCapacity {
		return fmt.Errorf("queue full - consumer too slow, backpressure at depth %d/%d",
			nextHead-consumerTail, MMQueueCapacity)
	}

	pos := producerHead % MMQueueCapacity
	q.orders[pos] = order

	// Publish after write; seq-cst store is sufficient
	atomic.StoreUint64(&q.header.ProducerHead, nextHead)
	return nil
}

func (q *MMQueue) Dequeue() (*MMOrder, error) {
	producerHead := atomic.LoadUint64(&q.header.ProducerHead)
	consumerTail := atomic.LoadUint64(&q.header.ConsumerTail)

	if consumerTail == producerHead {
		return nil, nil
	}

	pos := consumerTail % MMQueueCapacity
	order := q.orders[pos]

	// Mark consumed; seq-cst store is sufficient
	atomic.StoreUint64(&q.header.ConsumerTail, consumerTail+1)
	return &order, nil
}

func (q *MMQueue) Depth() uint64 {
	producerHead := atomic.LoadUint64(&q.header.ProducerHead)
	consumerTail := atomic.LoadUint64(&q.header.ConsumerTail)
	return producerHead - consumerTail
}

func (q *MMQueue) Capacity() uint64 {
	return MMQueueCapacity
}

func (q *MMQueue) Flush() error {
	return q.mmap.Flush()
}

func (q *MMQueue) Close() error {
	_ = q.mmap.Flush()
	_ = q.mmap.Unlock()
	if err := q.mmap.Unmap(); err != nil {
		_ = q.file.Close()
		return fmt.Errorf("failed to unmap: %w", err)
	}
	return q.file.Close()
}
