package queue

import (
	"encoding/binary"
	"log"
	"time"
)

const (
	// Number of bytes used to keep information about entry size
	headerEntrySize = 4
	// Bytes before left margin are not used. Zero index means element does not exist in queue, useful while reading slice from index
	leftMarginIndex = 1
	// Minimum empty blob size in bytes. Empty blob fills space between tail and head in additional memory allocation.
	// It keeps entries indexes unchanged
	minimumEmptyBlobSize = 32 + headerEntrySize
)

// BytesQueue is a non-thread safe queue type of fifo based on bytes array.
// For every push operation index of entry is returned. It can be used to read the entry later
type BytesQueue struct {
	array        []byte
	capacity     int
	head         int
	tail         int
	count        int
	rightMargin  int
	headerBuffer []byte
	verbose      bool
}

type queueError struct {
	message string
}

// NewBytesQueue initialize new bytes queue.
// Initial capacity is used in bytes array allocation
// When verbose flag is set then information about memory allocation are printed
func NewBytesQueue(initialCapacity int, verbose bool) *BytesQueue {
	return &BytesQueue{
		array:        make([]byte, initialCapacity),
		capacity:     initialCapacity,
		headerBuffer: make([]byte, headerEntrySize),
		tail:         leftMarginIndex,
		head:         leftMarginIndex,
		rightMargin:  leftMarginIndex,
		verbose:      verbose,
	}
}

// Push copies entry at the end of queue and moves tail pointer. Allocates more space if needed.
// Returns index for pushed data
func (q *BytesQueue) Push(data []byte) int {
	dataLen := len(data)

	if q.availableSpaceAfterTail() < dataLen+headerEntrySize {
		if q.availableSpaceBeforeHead() >= dataLen+headerEntrySize {
			q.tail = leftMarginIndex
		} else {
			q.allocateAdditionalMemory(dataLen)
		}
	}

	index := q.tail

	q.push(data, dataLen)

	return index
}

func (q *BytesQueue) allocateAdditionalMemory(minimum int) {
	start := time.Now()
	if q.capacity < minimum {
		q.capacity += minimum
	}
	q.capacity = q.capacity * 2
	oldArray := q.array
	q.array = make([]byte, q.capacity)

	if leftMarginIndex != q.rightMargin {
		copy(q.array, oldArray[:q.rightMargin])

		if q.tail < q.head {
			emptyBlobLen := q.head - q.tail - headerEntrySize
			q.push(make([]byte, emptyBlobLen), emptyBlobLen)
			q.head = leftMarginIndex
			q.tail = q.rightMargin
		}
	}

	if q.verbose {
		log.Printf("Allocated new queue in %s; Capacity: %d \n", time.Since(start), q.capacity)
	}
}

func (q *BytesQueue) push(data []byte, len int) {
	binary.LittleEndian.PutUint32(q.headerBuffer, uint32(len))
	q.copy(q.headerBuffer, headerEntrySize)

	q.copy(data, len)

	if q.tail > q.head {
		q.rightMargin = q.tail
	}

	q.count++
}

func (q *BytesQueue) copy(data []byte, len int) {
	q.tail += copy(q.array[q.tail:], data[:len])
}

// Pop reads the oldest entry from queue and moves head pointer to the next one
func (q *BytesQueue) Pop() ([]byte, error) {
	if q.count == 0 {
		return nil, &queueError{"Empty queue"}
	}

	data, size := q.peek(q.head)

	q.head += headerEntrySize + size
	q.count--

	if q.head == q.rightMargin {
		q.head = leftMarginIndex
		if q.tail == q.rightMargin {
			q.tail = leftMarginIndex
		}
		q.rightMargin = q.tail
	}

	return data, nil
}

func (q *BytesQueue) Clear() {
	q.head = leftMarginIndex
	q.tail = leftMarginIndex
	q.rightMargin = leftMarginIndex
	q.count = 0
}

// Peek reads the oldest entry from list without moving head pointer
func (q *BytesQueue) Peek() ([]byte, error) {
	if q.count == 0 {
		return nil, &queueError{"Empty queue"}
	}

	data, _ := q.peek(q.head)

	return data, nil
}

// Get reads entry from index
func (q *BytesQueue) Get(index int) ([]byte, error) {
	if index <= 0 {
		return nil, &queueError{"Index must be grater than zero. Invalid index."}
	}

	data, _ := q.peek(index)
	return data, nil
}

// Capacity returns number of allocated bytes for queue
func (q *BytesQueue) Capacity() int {
	return q.capacity
}

// Len returns number of entries kept in queue
func (q *BytesQueue) Len() int {
	return q.count
}

// Error returns error message
func (e *queueError) Error() string {
	return e.message
}

func (q *BytesQueue) peek(index int) ([]byte, int) {
	blockSize := int(binary.LittleEndian.Uint32(q.array[index : index+headerEntrySize]))
	return q.array[index+headerEntrySize : index+headerEntrySize+blockSize], blockSize
}

func (q *BytesQueue) availableSpaceAfterTail() int {
	if q.tail >= q.head {
		return q.capacity - q.tail
	}
	return q.head - q.tail - minimumEmptyBlobSize
}

func (q *BytesQueue) availableSpaceBeforeHead() int {
	if q.tail >= q.head {
		return q.head - leftMarginIndex - minimumEmptyBlobSize
	}
	return q.head - q.tail - minimumEmptyBlobSize
}
