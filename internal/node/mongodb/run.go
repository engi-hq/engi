package mongodb

import (
	"context"
	"errors"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// run executes one request and returns the item JSON documents it yields.
func run(ctx context.Context, coll *mongo.Collection, r request) ([]map[string]any, error) {
	switch r.op {
	case opFind:
		opts := options.Find().SetLimit(r.limit)
		if len(r.projection) > 0 {
			opts.SetProjection(r.projection)
		}
		if len(r.sort) > 0 {
			opts.SetSort(r.sort)
		}
		cur, err := coll.Find(ctx, r.filter, opts)
		if err != nil {
			return nil, fmt.Errorf("find: %w", err)
		}
		return drain(ctx, cur)
	case opAggregate:
		cur, err := coll.Aggregate(ctx, append(r.pipeline, bson.D{{Key: "$limit", Value: r.limit}}))
		if err != nil {
			return nil, fmt.Errorf("aggregate: %w", err)
		}
		return drain(ctx, cur)
	case opFindOneUpdate:
		return findOneAndUpdate(ctx, coll, r)
	case opInsert:
		res, err := coll.InsertMany(ctx, r.documents)
		if err != nil {
			return nil, fmt.Errorf("insert: %w", err)
		}
		return summary(bson.D{{Key: "insertedCount", Value: len(res.InsertedIDs)}, {Key: "insertedIds", Value: res.InsertedIDs}})
	case opUpdate:
		var res *mongo.UpdateResult
		var err error
		if r.multiple {
			res, err = coll.UpdateMany(ctx, r.filter, r.update, options.UpdateMany().SetUpsert(r.upsert))
		} else {
			res, err = coll.UpdateOne(ctx, r.filter, r.update, options.UpdateOne().SetUpsert(r.upsert))
		}
		if err != nil {
			return nil, fmt.Errorf("update: %w", err)
		}
		return summary(bson.D{
			{Key: "matchedCount", Value: res.MatchedCount}, {Key: "modifiedCount", Value: res.ModifiedCount},
			{Key: "upsertedCount", Value: res.UpsertedCount}, {Key: "upsertedId", Value: res.UpsertedID},
		})
	case opDelete:
		var res *mongo.DeleteResult
		var err error
		if r.multiple {
			res, err = coll.DeleteMany(ctx, r.filter)
		} else {
			res, err = coll.DeleteOne(ctx, r.filter)
		}
		if err != nil {
			return nil, fmt.Errorf("delete: %w", err)
		}
		return summary(bson.D{{Key: "deletedCount", Value: res.DeletedCount}})
	}
	return nil, fmt.Errorf("unknown operation %q", r.op)
}

func findOneAndUpdate(ctx context.Context, coll *mongo.Collection, r request) ([]map[string]any, error) {
	opts := options.FindOneAndUpdate().SetUpsert(r.upsert)
	if r.after {
		opts.SetReturnDocument(options.After)
	} else {
		opts.SetReturnDocument(options.Before)
	}
	if len(r.projection) > 0 {
		opts.SetProjection(r.projection)
	}
	if len(r.sort) > 0 {
		opts.SetSort(r.sort)
	}
	raw, err := coll.FindOneAndUpdate(ctx, r.filter, r.update, opts).Raw()
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("find one and update: %w", err)
	}
	m, err := plain(raw)
	if err != nil {
		return nil, err
	}
	return []map[string]any{m}, nil
}

// drain reads every document of the cursor and closes it.
func drain(ctx context.Context, cur *mongo.Cursor) ([]map[string]any, error) {
	defer func() { _ = cur.Close(ctx) }()
	out := []map[string]any{}
	for cur.Next(ctx) {
		m, err := plain(cur.Current)
		if err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	if err := cur.Err(); err != nil {
		return nil, fmt.Errorf("reading results: %w", err)
	}
	return out, nil
}

func summary(d bson.D) ([]map[string]any, error) {
	m, err := plain(d)
	if err != nil {
		return nil, err
	}
	return []map[string]any{m}, nil
}
