package helper

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/cago-frame/cago/pkg/logger"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.uber.org/zap"
)

// mongoExecutorV2 是 mongoDBExecutor 的 v2 驱动实现（默认路径，MongoDB 4.2+）。
// 只做"extended JSON <-> 本驱动 bson"的转换与驱动调用，不含任何操作逻辑。
type mongoExecutorV2 struct {
	client *mongo.Client
}

func (e *mongoExecutorV2) ListDatabases(ctx context.Context) ([]string, error) {
	names, err := e.client.ListDatabaseNames(ctx, bson.D{})
	if err != nil {
		return nil, fmt.Errorf("列出数据库失败: %w", err)
	}
	return names, nil
}

func (e *mongoExecutorV2) ListCollections(ctx context.Context, database string) ([]string, error) {
	names, err := e.client.Database(database).ListCollectionNames(ctx, bson.D{})
	if err != nil {
		return nil, fmt.Errorf("列出集合失败: %w", err)
	}
	return names, nil
}

func (e *mongoExecutorV2) Find(ctx context.Context, database, collection string, filter, sort, projection json.RawMessage, limit, skip int64) ([]json.RawMessage, error) {
	f, err := toBSONDoc(filter)
	if err != nil {
		return nil, fmt.Errorf("解析 filter 失败: %w", err)
	}
	if f == nil {
		f = bson.D{}
	}

	findOpts := options.Find()
	if sort != nil {
		var sortDoc bson.D
		if err := bson.UnmarshalExtJSON(sort, false, &sortDoc); err != nil {
			return nil, fmt.Errorf("解析 sort 失败: %w", err)
		}
		findOpts.SetSort(sortDoc)
	}
	if projection != nil {
		var projDoc bson.D
		if err := bson.UnmarshalExtJSON(projection, false, &projDoc); err != nil {
			return nil, fmt.Errorf("解析 projection 失败: %w", err)
		}
		findOpts.SetProjection(projDoc)
	}
	findOpts.SetLimit(limit)
	if skip > 0 {
		findOpts.SetSkip(skip)
	}

	coll := e.client.Database(database).Collection(collection)
	cursor, err := coll.Find(ctx, f, findOpts)
	if err != nil {
		return nil, fmt.Errorf("MongoDB find 失败: %w", err)
	}
	defer func() {
		if err := cursor.Close(ctx); err != nil {
			logger.Default().Warn("close MongoDB cursor", zap.Error(err))
		}
	}()

	return cursorToJSON(ctx, cursor)
}

func (e *mongoExecutorV2) FindOne(ctx context.Context, database, collection string, filter, projection json.RawMessage) (json.RawMessage, bool, error) {
	f, err := toBSONDoc(filter)
	if err != nil {
		return nil, false, fmt.Errorf("解析 filter 失败: %w", err)
	}
	if f == nil {
		f = bson.D{}
	}

	findOneOpts := options.FindOne()
	if projection != nil {
		var projDoc bson.D
		if err := bson.UnmarshalExtJSON(projection, false, &projDoc); err != nil {
			return nil, false, fmt.Errorf("解析 projection 失败: %w", err)
		}
		findOneOpts.SetProjection(projDoc)
	}

	coll := e.client.Database(database).Collection(collection)
	var doc bson.D
	err = coll.FindOne(ctx, f, findOneOpts).Decode(&doc)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("MongoDB findOne 失败: %w", err)
	}

	jsonDoc, err := bsonDocToJSON(doc)
	if err != nil {
		return nil, false, err
	}
	return jsonDoc, true, nil
}

func (e *mongoExecutorV2) InsertOne(ctx context.Context, database, collection string, document json.RawMessage) (string, error) {
	var doc bson.D
	if err := bson.UnmarshalExtJSON(document, false, &doc); err != nil {
		return "", fmt.Errorf("解析 document 失败: %w", err)
	}

	coll := e.client.Database(database).Collection(collection)
	result, err := coll.InsertOne(ctx, doc)
	if err != nil {
		return "", fmt.Errorf("MongoDB insertOne 失败: %w", err)
	}
	return fmt.Sprint(result.InsertedID), nil
}

func (e *mongoExecutorV2) InsertMany(ctx context.Context, database, collection string, documents []json.RawMessage) ([]string, error) {
	docs := make([]any, len(documents))
	for i, raw := range documents {
		var doc bson.D
		if err := bson.UnmarshalExtJSON(raw, false, &doc); err != nil {
			return nil, fmt.Errorf("解析 documents[%d] 失败: %w", i, err)
		}
		docs[i] = doc
	}

	coll := e.client.Database(database).Collection(collection)
	result, err := coll.InsertMany(ctx, docs)
	if err != nil {
		return nil, fmt.Errorf("MongoDB insertMany 失败: %w", err)
	}

	ids := make([]string, len(result.InsertedIDs))
	for i, id := range result.InsertedIDs {
		ids[i] = fmt.Sprint(id)
	}
	return ids, nil
}

func (e *mongoExecutorV2) UpdateOne(ctx context.Context, database, collection string, filter, update json.RawMessage) (int64, int64, error) {
	f, u, err := parseUpdateArgs(filter, update)
	if err != nil {
		return 0, 0, err
	}
	coll := e.client.Database(database).Collection(collection)
	result, err := coll.UpdateOne(ctx, f, u)
	if err != nil {
		return 0, 0, fmt.Errorf("MongoDB updateOne 失败: %w", err)
	}
	return result.MatchedCount, result.ModifiedCount, nil
}

func (e *mongoExecutorV2) UpdateMany(ctx context.Context, database, collection string, filter, update json.RawMessage) (int64, int64, error) {
	f, u, err := parseUpdateArgs(filter, update)
	if err != nil {
		return 0, 0, err
	}
	coll := e.client.Database(database).Collection(collection)
	result, err := coll.UpdateMany(ctx, f, u)
	if err != nil {
		return 0, 0, fmt.Errorf("MongoDB updateMany 失败: %w", err)
	}
	return result.MatchedCount, result.ModifiedCount, nil
}

func (e *mongoExecutorV2) DeleteOne(ctx context.Context, database, collection string, filter json.RawMessage) (int64, error) {
	f, err := toBSONDoc(filter)
	if err != nil {
		return 0, fmt.Errorf("解析 filter 失败: %w", err)
	}
	if f == nil {
		f = bson.D{}
	}
	coll := e.client.Database(database).Collection(collection)
	result, err := coll.DeleteOne(ctx, f)
	if err != nil {
		return 0, fmt.Errorf("MongoDB deleteOne 失败: %w", err)
	}
	return result.DeletedCount, nil
}

func (e *mongoExecutorV2) DeleteMany(ctx context.Context, database, collection string, filter json.RawMessage) (int64, error) {
	f, err := toBSONDoc(filter)
	if err != nil {
		return 0, fmt.Errorf("解析 filter 失败: %w", err)
	}
	if f == nil {
		f = bson.D{}
	}
	coll := e.client.Database(database).Collection(collection)
	result, err := coll.DeleteMany(ctx, f)
	if err != nil {
		return 0, fmt.Errorf("MongoDB deleteMany 失败: %w", err)
	}
	return result.DeletedCount, nil
}

func (e *mongoExecutorV2) Aggregate(ctx context.Context, database, collection string, pipeline json.RawMessage) ([]json.RawMessage, error) {
	var rawStages []json.RawMessage
	if err := json.Unmarshal(pipeline, &rawStages); err != nil {
		return nil, fmt.Errorf("解析 pipeline 数组失败: %w", err)
	}

	stages := make(bson.A, len(rawStages))
	for i, raw := range rawStages {
		var stage bson.D
		if err := bson.UnmarshalExtJSON(raw, false, &stage); err != nil {
			return nil, fmt.Errorf("解析 pipeline[%d] 失败: %w", i, err)
		}
		stages[i] = stage
	}

	coll := e.client.Database(database).Collection(collection)
	cursor, err := coll.Aggregate(ctx, stages)
	if err != nil {
		return nil, fmt.Errorf("MongoDB aggregate 失败: %w", err)
	}
	defer func() {
		if err := cursor.Close(ctx); err != nil {
			logger.Default().Warn("close MongoDB cursor", zap.Error(err))
		}
	}()

	return cursorToJSON(ctx, cursor)
}

func (e *mongoExecutorV2) CountDocuments(ctx context.Context, database, collection string, filter json.RawMessage) (int64, error) {
	f, err := toBSONDoc(filter)
	if err != nil {
		return 0, fmt.Errorf("解析 filter 失败: %w", err)
	}
	if f == nil {
		f = bson.D{}
	}
	coll := e.client.Database(database).Collection(collection)
	count, err := coll.CountDocuments(ctx, f)
	if err != nil {
		return 0, fmt.Errorf("MongoDB countDocuments 失败: %w", err)
	}
	return count, nil
}

// parseUpdateArgs 解析 update 的 filter/update 文档。update 是否缺失由共享层的
// mongoUpdateOne/mongoUpdateMany 在带操作名的报错里判断，这里只做 bson 转换。
func parseUpdateArgs(filter, update json.RawMessage) (bson.D, bson.D, error) {
	f, err := toBSONDoc(filter)
	if err != nil {
		return nil, nil, fmt.Errorf("解析 filter 失败: %w", err)
	}
	if f == nil {
		f = bson.D{}
	}
	u, err := toBSONDoc(update)
	if err != nil {
		return nil, nil, fmt.Errorf("解析 update 失败: %w", err)
	}
	return f, u, nil
}

// toBSONDoc 把 extended JSON 解析为 bson.D。raw 为 nil 时返回 (nil, nil)。
func toBSONDoc(raw json.RawMessage) (bson.D, error) {
	if raw == nil {
		return nil, nil
	}
	var doc bson.D
	if err := bson.UnmarshalExtJSON(raw, false, &doc); err != nil {
		return nil, err
	}
	return doc, nil
}

// cursorToJSON 遍历 MongoDB cursor，将每个文档转换为 json.RawMessage
func cursorToJSON(ctx context.Context, cursor *mongo.Cursor) ([]json.RawMessage, error) {
	var docs []json.RawMessage
	for cursor.Next(ctx) {
		var doc bson.D
		if err := cursor.Decode(&doc); err != nil {
			return nil, fmt.Errorf("解码文档失败: %w", err)
		}
		jsonBytes, err := bson.MarshalExtJSON(doc, false, false)
		if err != nil {
			return nil, fmt.Errorf("转换文档为 JSON 失败: %w", err)
		}
		docs = append(docs, jsonBytes)
	}
	if err := cursor.Err(); err != nil {
		return nil, fmt.Errorf("cursor 迭代错误: %w", err)
	}
	return docs, nil
}

// bsonDocToJSON 将 bson.D 转换为 json.RawMessage
func bsonDocToJSON(doc bson.D) (json.RawMessage, error) {
	jsonBytes, err := bson.MarshalExtJSON(doc, false, false)
	if err != nil {
		return nil, fmt.Errorf("转换文档为 JSON 失败: %w", err)
	}
	return jsonBytes, nil
}
