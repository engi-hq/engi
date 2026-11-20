package domain

// Item is the unit of data flowing between nodes.
type Item struct {
	JSON map[string]any
	// Binary holds references to blobs. Bytes never travel inside items.
	Binary map[string]BinaryRef
	// PairedItem records which input items produced this item.
	PairedItem []ItemRef
	// Error is set on items that carry a failure (onError continue/errorOutput).
	Error *ErrorInfo
}

// BinaryRef points at a blob in the blob store.
type BinaryRef struct {
	Ref  string
	MIME string
	Name string
	Size int64
}

// ItemRef identifies an item of a node's input.
type ItemRef struct {
	// Input is the input port index.
	Input int
	// Item is the item index within that input.
	Item int
}
