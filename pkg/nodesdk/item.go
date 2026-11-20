package nodesdk

// Item is the unit of data flowing between nodes. A node receives []Item per
// input port and returns []Item per output port.
type Item struct {
	JSON map[string]any
	// Binary holds references to blobs; bytes never travel inside items.
	Binary map[string]BinaryRef
	// PairedItem records which input items produced this item. If a node leaves
	// it empty the engine fills it in when the mapping is unambiguous.
	PairedItem []PairedItem
	// Error is set on items that carry a failure.
	Error *ItemError
}

// BinaryRef points at a blob in the blob store.
type BinaryRef struct {
	Ref  string
	MIME string
	Name string
	Size int64
}

// PairedItem identifies an input item.
type PairedItem struct {
	Input int
	Item  int
}

// ItemError describes a failure attached to an item.
type ItemError struct {
	// Node names the node that produced the failure; empty means the node
	// carrying the item.
	Node        string
	Message     string
	Description string
	HTTPStatus  int
}
