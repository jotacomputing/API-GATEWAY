package shm
import (
	"fmt"
	"os"
	"sync/atomic"
	"unsafe"
	"github.com/edsrzf/mmap-go"
)

type Message struct {
	OrderId uint64
	ClientId uint64
	Timestamp uint64
	Symbol uint32
	MessageType uint8 // 0 -> add this symbol  , 1-> order placed ack , 2-> order canceld ack 
}

type MessageQueueHeader struct {
	ProducerHead uint64   // Offset 0 4 byte interger 
	_        [56]byte // Padding to cache line
	ConsumerTail uint64   // Offset 64
	_      [56]byte // Padding
	Magic        uint32   // Offset 128
	Capacity     uint32   // Offset 132
}

const (
	MMResponseQueueMagic    = 0xEAAAAAA2 
	MMResponseQueueCapacity = 65536
	MessageSize       = unsafe.Sizeof(Message{})
	MessageHeaderSize = unsafe.Sizeof(MessageQueueHeader{})
	MessageTotalSize  = MessageHeaderSize + (MMResponseQueueCapacity * MessageSize)
)


type MMResponseQueue struct {
	file   *os.File
	mmap   mmap.MMap   // this is the array of bytes wich we will use to read and write 
	header *MessageQueueHeader
	messages []Message
}

func CreateMMResponseQueue(path string) (*MMResponseQueue, error) {
	_ = os.Remove(path)

	file, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o666)
	if err != nil {
		return nil, err
	}

	if err := file.Truncate(int64(MessageTotalSize)); err != nil {
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

	header := (*MessageQueueHeader)(unsafe.Pointer(&m[0]))
	atomic.StoreUint64(&header.ProducerHead, 0)
	atomic.StoreUint64(&header.ConsumerTail, 0)
	atomic.StoreUint32(&header.Magic, MMResponseQueueMagic)
	atomic.StoreUint32(&header.Capacity, MMResponseQueueCapacity)
	data := m[MessageHeaderSize:MessageTotalSize]
	messages := unsafe.Slice(
		(*Message)(unsafe.Pointer(&data[0])),
		MMResponseQueueCapacity,
	)

	return &MMResponseQueue{
		file:    file,
		mmap:    m,
		header:  header,
		messages: messages,
	}, nil
}

// open queue from file on disk and return *Queue mmap-ed
func OpenMMResponseQueue(path string) (*MMResponseQueue, error) {
	file, err := os.OpenFile(path, os.O_RDWR, 0o666)
	if err != nil {
		return nil, err
	}

	stat, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}

	if stat.Size() != int64(MessageTotalSize) {
		file.Close()
		return nil, fmt.Errorf("message queue size mismatch")
	}

	m, err := mmap.Map(file, mmap.RDWR, 0)
	if err != nil {
		file.Close()
		return nil, err
	}

	_ = m.Lock()

	header := (*MessageQueueHeader)(unsafe.Pointer(&m[0]))
	if atomic.LoadUint32(&header.Magic) != MMResponseQueueMagic {
		return nil, fmt.Errorf("invalid message queue magic")
	}

	data := m[MessageHeaderSize:MessageTotalSize]
	messages := unsafe.Slice(
		(*Message)(unsafe.Pointer(&data[0])),
		MMResponseQueueCapacity,
	)

	return &MMResponseQueue{
		file:    file,
		mmap:    m,
		header:  header,
		messages: messages,
	}, nil
}


func (q *MMResponseQueue) Enqueue(message Message) error {
	consumer := atomic.LoadUint64(&q.header.ConsumerTail)
	producer := atomic.LoadUint64(&q.header.ProducerHead)

	if producer-consumer >= MMResponseQueueCapacity {
		return fmt.Errorf("message queue full")
	}

	pos := producer % MMResponseQueueCapacity
	q.messages[pos] = message

	atomic.StoreUint64(&q.header.ProducerHead, producer+1)
	return nil
}


func (q *MMResponseQueue) Dequeue() (*Message, error) {
	producer := atomic.LoadUint64(&q.header.ProducerHead)
	consumer := atomic.LoadUint64(&q.header.ConsumerTail)

	if consumer == producer {
		return nil, nil
	}

	pos := consumer % MMResponseQueueCapacity
	message := q.messages[pos]		
	atomic.StoreUint64(&q.header.ConsumerTail, consumer+1)
	return &message, nil
}


func (q *MMResponseQueue) Depth() uint64 {
	return atomic.LoadUint64(&q.header.ProducerHead) -
		atomic.LoadUint64(&q.header.ConsumerTail)
}

func (q *MMResponseQueue) Capacity() uint64 {
	return MMResponseQueueCapacity
}

func (q *MMResponseQueue) Flush() error {
	return q.mmap.Flush()
}

func (q *MMResponseQueue) Close() error {
	_ = q.mmap.Flush()
	_ = q.mmap.Unlock()
	_ = q.mmap.Unmap()
	return q.file.Close()
}
