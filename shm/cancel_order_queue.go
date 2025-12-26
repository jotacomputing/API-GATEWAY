package shm
import (
	"fmt"
	"os"
	"sync/atomic"
	"unsafe"
	"github.com/edsrzf/mmap-go"
)

type OrderToBeCanceled struct {
	// All uint64s first (8-byte aligned)
	OrderId uint64 
	UserId  uint64 
	Symbol 	uint32
	_    [4]byte
}

type TempOrderToBeCanceled struct {
    OrderId uint64 `json:"order_id"`
    Symbol  uint32 `json:"symbol"`
}

type CancelOrderQueueHeader struct {
	ProducerHead uint64   // Offset 0 4 byte interger 
	_        [56]byte // Padding to cache line
	ConsumerTail uint64   // Offset 64
	_      [56]byte // Padding
	Magic        uint32   // Offset 128
	Capacity     uint32   // Offset 132
}

const (
	CancelQueueMagic    = 0xCACECE
	CancelQueueCapacity = 65536

	CancelOrderSize       = unsafe.Sizeof(OrderToBeCanceled{})
	CancelHeaderSize      = unsafe.Sizeof(CancelOrderQueueHeader{})
	CancelQueueTotalSize  = CancelHeaderSize +
		(CancelQueueCapacity * CancelOrderSize)
)


type CancelOrderQueue struct {
	file   *os.File
	mmap   mmap.MMap
	header *CancelOrderQueueHeader
	orders []OrderToBeCanceled
}


func CreateCancelOrderQueue(path string) (*CancelOrderQueue, error) {
	_ = os.Remove(path)

	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o666)
	if err != nil {
		return nil, err
	}

	if err := file.Truncate(int64(CancelQueueTotalSize)); err != nil {
		file.Close()
		return nil, err
	}

	if err := file.Sync(); err != nil {
		file.Close()
		return nil, err
	}

	m, err := mmap.Map(file, mmap.RDWR, 0)
	if err != nil {
		file.Close()
		return nil, err
	}

	_ = m.Lock()

	header := (*CancelOrderQueueHeader)(unsafe.Pointer(&m[0]))
	atomic.StoreUint64(&header.ProducerHead, 0)
	atomic.StoreUint64(&header.ConsumerTail, 0)
	atomic.StoreUint32(&header.Magic, CancelQueueMagic)
	atomic.StoreUint32(&header.Capacity, CancelQueueCapacity)

	data := m[CancelHeaderSize:CancelQueueTotalSize]
	orders := unsafe.Slice(
		(*OrderToBeCanceled)(unsafe.Pointer(&data[0])),
		CancelQueueCapacity,
	)

	return &CancelOrderQueue{
		file:   file,
		mmap:   m,
		header: header,
		orders: orders,
	}, nil
}


// open queue from file on disk and return *Queue mmap-ed
func OpenCancelOrderQueue(path string) (*CancelOrderQueue, error) {
	file, err := os.OpenFile(path, os.O_RDWR, 0o666)
	if err != nil {
		return nil, err
	}

	stat, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}

	if stat.Size() != int64(CancelQueueTotalSize) {
		file.Close()
		return nil, fmt.Errorf("size mismatch")
	}

	m, err := mmap.Map(file, mmap.RDWR, 0)
	if err != nil {
		file.Close()
		return nil, err
	}

	_ = m.Lock()

	header := (*CancelOrderQueueHeader)(unsafe.Pointer(&m[0]))
	if atomic.LoadUint32(&header.Magic) != CancelQueueMagic {
		return nil, fmt.Errorf("invalid cancel queue magic")
	}

	data := m[CancelHeaderSize:CancelQueueTotalSize]
	orders := unsafe.Slice(
		(*OrderToBeCanceled)(unsafe.Pointer(&data[0])),
		CancelQueueCapacity,
	)

	return &CancelOrderQueue{
		file:   file,
		mmap:   m,
		header: header,
		orders: orders,
	}, nil
}


func (q *CancelOrderQueue) Enqueue(order OrderToBeCanceled) error {
	consumer := atomic.LoadUint64(&q.header.ConsumerTail)
	producer := atomic.LoadUint64(&q.header.ProducerHead)

	if producer-consumer >= CancelQueueCapacity {
		return fmt.Errorf("cancel queue full")
	}

	pos := producer % CancelQueueCapacity
	q.orders[pos] = order

	atomic.StoreUint64(&q.header.ProducerHead, producer+1)
	return nil
}


func (q *CancelOrderQueue) Dequeue() (*OrderToBeCanceled, error) {
	producer := atomic.LoadUint64(&q.header.ProducerHead)
	consumer := atomic.LoadUint64(&q.header.ConsumerTail)

	if consumer == producer {
		return nil, nil
	}

	pos := consumer % CancelQueueCapacity
	order := q.orders[pos]

	atomic.StoreUint64(&q.header.ConsumerTail, consumer+1)
	return &order, nil
}


func (q *CancelOrderQueue) Depth() uint64 {
	return atomic.LoadUint64(&q.header.ProducerHead) -
		atomic.LoadUint64(&q.header.ConsumerTail)
}

func (q *CancelOrderQueue) Capacity() uint64 {
	return CancelQueueCapacity
}

func (q *CancelOrderQueue) Flush() error {
	return q.mmap.Flush()
}

func (q *CancelOrderQueue) Close() error {
	_ = q.mmap.Flush()
	_ = q.mmap.Unlock()
	_ = q.mmap.Unmap()
	return q.file.Close()
}
