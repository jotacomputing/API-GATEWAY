package shm

import (
	"fmt"
	"os"
	"sync/atomic"
	"unsafe"

	"github.com/edsrzf/mmap-go"
)

const MAX_SYMBOLS = 100

type UserHoldings struct {
	UserId             uint64
	AvailableHoldings  [MAX_SYMBOLS]uint32
	ReservedHoldings   [MAX_SYMBOLS]uint32
}


type HoldingResponse struct {
	UserId   uint64
	Symbol   uint32
	delta_available_holdings  int32
	delta_reserved_holdings   int32
	_pad	 [4]byte
}


type HoldingResponseQueueHeader struct {
	ProducerHead uint64
	_pad1        [56]byte
	ConsumerTail uint64
	_pad2        [56]byte
	Magic        uint32
	Capacity     uint32
}

const (
	HoldingsQueueMagic    = 0xCECAEAAC 
	HoldingsQueueCapacity = 65536

	HoldingResponseSize       = unsafe.Sizeof(HoldingResponse{})
	HoldingsHeaderSize        = unsafe.Sizeof(HoldingResponseQueueHeader{})
	HoldingsQueueTotalSize   = HoldingsHeaderSize +
		(HoldingsQueueCapacity * HoldingResponseSize)
)

type HoldingResponseQueue struct {
	file     *os.File
	mmap     mmap.MMap
	header   *HoldingResponseQueueHeader
	response []HoldingResponse
}

func CreateHoldingResponseQueue(path string) (*HoldingResponseQueue, error) {
	_ = os.Remove(path)

	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o666)
	if err != nil {
		return nil, err
	}

	if err := file.Truncate(int64(HoldingsQueueTotalSize)); err != nil {
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

	header := (*HoldingResponseQueueHeader)(unsafe.Pointer(&m[0]))
	atomic.StoreUint64(&header.ProducerHead, 0)
	atomic.StoreUint64(&header.ConsumerTail, 0)
	atomic.StoreUint32(&header.Magic, HoldingsQueueMagic)
	atomic.StoreUint32(&header.Capacity, HoldingsQueueCapacity)

	data := m[HoldingsHeaderSize:HoldingsQueueTotalSize]
	response := unsafe.Slice(
		(*HoldingResponse)(unsafe.Pointer(&data[0])),
		HoldingsQueueCapacity,
	)

	return &HoldingResponseQueue{
		file:     file,
		mmap:     m,
		header:   header,
		response: response,
	}, nil
}



func OpenHoldingResponseQueue(path string) (*HoldingResponseQueue, error) {
	file, err := os.OpenFile(path, os.O_RDWR, 0o666)
	if err != nil {
		return nil, err
	}

	stat, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}

	if stat.Size() != int64(HoldingsQueueTotalSize) {
		file.Close()
		return nil, fmt.Errorf("holding queue size mismatch")
	}

	m, err := mmap.Map(file, mmap.RDWR, 0)
	if err != nil {
		file.Close()
		return nil, err
	}

	_ = m.Lock()

	header := (*HoldingResponseQueueHeader)(unsafe.Pointer(&m[0]))
	if atomic.LoadUint32(&header.Magic) != HoldingsQueueMagic {
		return nil, fmt.Errorf("invalid holdings queue magic")
	}

	data := m[HoldingsHeaderSize:HoldingsQueueTotalSize]
	response := unsafe.Slice(
		(*HoldingResponse)(unsafe.Pointer(&data[0])),
		HoldingsQueueCapacity,
	)

	return &HoldingResponseQueue{
		file:     file,
		mmap:     m,
		header:   header,
		response: response,
	}, nil
}


func (q *HoldingResponseQueue) Enqueue(resp HoldingResponse) error {
	consumer := atomic.LoadUint64(&q.header.ConsumerTail)
	producer := atomic.LoadUint64(&q.header.ProducerHead)

	if producer-consumer >= HoldingsQueueCapacity {
		return fmt.Errorf("holdings response queue full")
	}

	pos := producer % HoldingsQueueCapacity
	q.response[pos] = resp

	atomic.StoreUint64(&q.header.ProducerHead, producer+1)
	return nil
}


func (q *HoldingResponseQueue) Dequeue() (*HoldingResponse, error) {
	producer := atomic.LoadUint64(&q.header.ProducerHead)
	consumer := atomic.LoadUint64(&q.header.ConsumerTail)

	if consumer == producer {
		return nil, nil
	}

	pos := consumer % HoldingsQueueCapacity
	resp := q.response[pos]

	atomic.StoreUint64(&q.header.ConsumerTail, consumer+1)
	return &resp, nil
}

func (q *HoldingResponseQueue) Depth() uint64 {
	return atomic.LoadUint64(&q.header.ProducerHead) -
		atomic.LoadUint64(&q.header.ConsumerTail)
}

func (q *HoldingResponseQueue) Flush() error {
	return q.mmap.Flush()
}

func (q *HoldingResponseQueue) Close() error {
	_ = q.mmap.Flush()
	_ = q.mmap.Unlock()
	_ = q.mmap.Unmap()
	return q.file.Close()
}
