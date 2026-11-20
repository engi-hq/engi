package mongodb

import (
	"encoding/json"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// plain converts a BSON document to item JSON through relaxed extended JSON:
// an ObjectId becomes {"$oid": "..."}, a date {"$date": "..."}, and so on, which
// can be pasted back into a filter.
func plain(doc any) (map[string]any, error) {
	raw, err := bson.MarshalExtJSON(doc, false, false)
	if err != nil {
		return nil, fmt.Errorf("encoding document: %w", err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("decoding document: %w", err)
	}
	return m, nil
}
