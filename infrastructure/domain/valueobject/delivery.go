package valueobject

import "fmt"

// Receipt is how far a reply sent to a chat got: delivered to the
// person's device, or read. Channels without receipts leave it empty.
type Receipt string

const (
	ReceiptNone      Receipt = ""
	ReceiptDelivered Receipt = "delivered"
	ReceiptRead      Receipt = "read"
)

// ParseReceipt accepts the two receipt states.
func ParseReceipt(s string) (Receipt, error) {
	switch r := Receipt(s); r {
	case ReceiptNone, ReceiptDelivered, ReceiptRead:
		return r, nil
	}
	return "", fmt.Errorf("unknown receipt %q", s)
}

// After reports whether r is further along than prev: read beats
// delivered, and a receipt never goes back.
func (r Receipt) After(prev Receipt) bool {
	rank := map[Receipt]int{ReceiptNone: 0, ReceiptDelivered: 1, ReceiptRead: 2}
	return rank[r] > rank[prev]
}
