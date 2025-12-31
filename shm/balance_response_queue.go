package shm
import (
	"fmt"
	"os"
	"sync/atomic"
	"unsafe"
	"github.com/edsrzf/mmap-go"
)

type UserBalance struct{
	 UserId 			uint64   // 8        
     Available_balance  uint64
     Reserved_balance   uint64
     Total_traded_today uint64
     Order_count_today	uint64
	 _pad				[24]byte
}
// user balance 64 bytes 

type BalanceResponse struct {
	UserId 	uint64
	delta_available_balance  int64
	delta_reserved_balance   int64
}

type BalanceResponseHeader struct {
	ProducerHead uint64   // Offset 0 4 byte interger 
	_        [56]byte // Padding to cache line
	ConsumerTail uint64   // Offset 64
	_      [56]byte // Padding
	Magic        uint32   // Offset 128
	Capacity     uint32   // Offset 132
}

const (
	BQueueMagic    = 0xDEADBEEA
	BQueueCapacity = 65536 // !!IMP: match Rust
	BalanceResponseSize     = unsafe.Sizeof(BalanceResponse{})
	BalanceResponseHeaderSize    = unsafe.Sizeof(BalanceResponseHeader{})
	BalanceResponseTotalSize     = HeaderSize + (BQueueCapacity * BalanceResponseSize)
)


type BalanceResponseQueue struct {
	file   *os.File
	mmap   mmap.MMap   // this is the array of bytes wich we will use to read and write 
	header *BalanceResponseHeader
	response []BalanceResponse
}

func CreateBalanceResponseQueue(path string) (*BalanceResponseQueue, error) {
	_ = os.Remove(path)

	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o666)
	if err != nil {
		return nil, err
	}

	if err := file.Truncate(int64(BalanceResponseTotalSize)); err != nil {
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

	header := (*BalanceResponseHeader)(unsafe.Pointer(&m[0]))
	atomic.StoreUint64(&header.ProducerHead, 0)
	atomic.StoreUint64(&header.ConsumerTail, 0)
	atomic.StoreUint32(&header.Magic, BQueueMagic)
	atomic.StoreUint32(&header.Capacity, BQueueCapacity)

	respData := m[BalanceResponseHeaderSize:BalanceResponseTotalSize]
	response := unsafe.Slice(
		(*BalanceResponse)(unsafe.Pointer(&respData[0])),
		BQueueCapacity,
	)

	return &BalanceResponseQueue{
		file:     file,
		mmap:     m,
		header:   header,
		response: response,
	}, nil
}


// open queue from file on disk and return *Queue mmap-ed
func OpenBalanceResponseQueue(path string) (*BalanceResponseQueue, error) {
	file, err := os.OpenFile(path, os.O_RDWR, 0o666)
	if err != nil {
		return nil, err
	}

	stat, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}

	if stat.Size() != int64(BalanceResponseTotalSize) {
		file.Close()
		return nil, fmt.Errorf("size mismatch")
	}

	m, err := mmap.Map(file, mmap.RDWR, 0)
	if err != nil {
		file.Close()
		return nil, err
	}

	_ = m.Lock()

	header := (*BalanceResponseHeader)(unsafe.Pointer(&m[0]))
	if atomic.LoadUint32(&header.Magic) != BQueueMagic {
		return nil, fmt.Errorf("invalid magic")
	}

	respData := m[BalanceResponseHeaderSize:BalanceResponseTotalSize]
	response := unsafe.Slice(
		(*BalanceResponse)(unsafe.Pointer(&respData[0])),
		BQueueCapacity,
	)

	return &BalanceResponseQueue{
		file:     file,
		mmap:     m,
		header:   header,
		response: response,
	}, nil
}


func (q *BalanceResponseQueue) Enqueue(resp BalanceResponse) error {
	consumer := atomic.LoadUint64(&q.header.ConsumerTail)
	producer := atomic.LoadUint64(&q.header.ProducerHead)

	if producer-consumer >= BQueueCapacity {
		return fmt.Errorf("queue full")
	}

	pos := producer % BQueueCapacity
	q.response[pos] = resp

	atomic.StoreUint64(&q.header.ProducerHead, producer+1)
	return nil
}


func (q *BalanceResponseQueue) Dequeue() (*BalanceResponse, error) {
	producer := atomic.LoadUint64(&q.header.ProducerHead)
	consumer := atomic.LoadUint64(&q.header.ConsumerTail)

	if consumer == producer {
		return nil, nil
	}

	pos := consumer % BQueueCapacity
	resp := q.response[pos]

	atomic.StoreUint64(&q.header.ConsumerTail, consumer+1)
	return &resp, nil
}


func (q *BalanceResponseQueue) Depth() uint64 {
	return atomic.LoadUint64(&q.header.ProducerHead) -
		atomic.LoadUint64(&q.header.ConsumerTail)
}

func (q *BalanceResponseQueue) Flush() error {
	return q.mmap.Flush()
}

func (q *BalanceResponseQueue) Close() error {
	_ = q.mmap.Flush()
	_ = q.mmap.Unlock()
	_ = q.mmap.Unmap()
	return q.file.Close()
}


