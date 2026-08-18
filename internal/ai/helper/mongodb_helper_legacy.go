package helper

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/cago-frame/cago/pkg/logger"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
	"go.uber.org/zap"
)

var mongoOpsV1 = map[string]mongoOpSpecV1{
	"find":            {needsCollection: true, needsDatabase: true, exec: mongoFindV1},
	"findOne":         {needsCollection: true, needsDatabase: true, exec: mongoFindOneV1},
	"insertOne":       {needsCollection: true, needsDatabase: true, exec: mongoInsertOneV1},
	"insertMany":      {needsCollection: true, needsDatabase: true, exec: mongoInsertManyV1},
	"updateOne":       {needsCollection: true, needsDatabase: true, exec: mongoUpdateOneV1},
	"updateMany":      {needsCollection: true, needsDatabase: true, exec: mongoUpdateManyV1},
	"deleteOne":       {needsCollection: true, needsDatabase: true, exec: mongoDeleteOneV1},
	"deleteMany":      {needsCollection: true, needsDatabase: true, exec: mongoDeleteManyV1},
	"aggregate":       {needsCollection: true, needsDatabase: true, exec: mongoAggregateV1},
	"countDocuments":  {needsCollection: true, needsDatabase: true, exec: mongoCountDocumentsV1},
	"listDatabases":   {needsCollection: false, needsDatabase: false, exec: mongoListDatabasesV1},
	"listCollections": {needsCollection: false, needsDatabase: true, exec: mongoListCollectionsV1},
}

type mongoOpSpecV1 struct {
	needsCollection bool
	needsDatabase   bool
	exec            func(ctx context.Context, client *mongo.Client, database, collection string, queryMap map[string]json.RawMessage) (string, error)
}

func executeMongoDBV1(ctx context.Context, client *mongo.Client, database, collection, operation, query string) (string, error) {
	queryMap, err := parseQueryMap(query)
	if err != nil {
		return "", fmt.Errorf("无效的查询参数: %w", err)
	}

	spec, ok := mongoOpsV1[operation]
	if !ok {
		return "", fmt.Errorf("不支持的 MongoDB 操作: %s", operation)
	}
	return spec.exec(ctx, client, database, collection, queryMap)
}

func listMongoDatabasesV1(ctx context.Context, client *mongo.Client) ([]string, error) {
	names, err := client.ListDatabaseNames(ctx, bson.D{})
	if err != nil {
		return nil, fmt.Errorf("列出数据库失败: %w", err)
	}
	return names, nil
}

func listMongoCollectionsV1(ctx context.Context, client *mongo.Client, database string) ([]string, error) {
	names, err := client.Database(database).ListCollectionNames(ctx, bson.D{})
	if err != nil {
		return nil, fmt.Errorf("列出集合失败: %w", err)
	}
	return names, nil
}

func mongoListDatabasesV1(ctx context.Context, client *mongo.Client, _, _ string, _ map[string]json.RawMessage) (string, error) {
	names, err := client.ListDatabaseNames(ctx, bson.D{})
	if err != nil {
		return "", fmt.Errorf("列出数据库失败: %w", err)
	}
	return marshalResult(map[string]any{"databases": names, "count": len(names)})
}

func mongoListCollectionsV1(ctx context.Context, client *mongo.Client, database, _ string, _ map[string]json.RawMessage) (string, error) {
	if database == "" {
		return "", fmt.Errorf("database 参数不能为空")
	}
	names, err := client.Database(database).ListCollectionNames(ctx, bson.D{})
	if err != nil {
		return "", fmt.Errorf("列出集合失败: %w", err)
	}
	return marshalResult(map[string]any{"collections": names, "count": len(names)})
}

func mongoFindV1(ctx context.Context, client *mongo.Client, database, collection string, queryMap map[string]json.RawMessage) (string, error) {
	if database == "" || collection == "" {
		return "", fmt.Errorf("find 操作需要 database 和 collection 参数")
	}

	filter, err := toBSONDocV1(queryMap, "filter")
	if err != nil {
		return "", fmt.Errorf("解析 filter 失败: %w", err)
	}
	if filter == nil {
		filter = bson.D{}
	}

	findOpts := options.Find()

	if sortRaw, ok := queryMap["sort"]; ok {
		var sortDoc bson.D
		if err := bson.UnmarshalExtJSON(sortRaw, false, &sortDoc); err != nil {
			return "", fmt.Errorf("解析 sort 失败: %w", err)
		}
		findOpts.SetSort(sortDoc)
	}

	if projRaw, ok := queryMap["projection"]; ok {
		var projDoc bson.D
		if err := bson.UnmarshalExtJSON(projRaw, false, &projDoc); err != nil {
			return "", fmt.Errorf("解析 projection 失败: %w", err)
		}
		findOpts.SetProjection(projDoc)
	}

	limit := int64(100)
	if limitRaw, ok := queryMap["limit"]; ok {
		var l int64
		if err := json.Unmarshal(limitRaw, &l); err == nil && l > 0 {
			limit = l
		}
	}
	findOpts.SetLimit(limit)

	if skipRaw, ok := queryMap["skip"]; ok {
		var s int64
		if err := json.Unmarshal(skipRaw, &s); err == nil && s >= 0 {
			findOpts.SetSkip(s)
		}
	}

	coll := client.Database(database).Collection(collection)
	cursor, err := coll.Find(ctx, filter, findOpts)
	if err != nil {
		return "", fmt.Errorf("MongoDB find 失败: %w", err)
	}
	defer func() {
		if err := cursor.Close(ctx); err != nil {
			logger.Default().Warn("close MongoDB cursor", zap.Error(err))
		}
	}()

	docs, err := cursorToJSONV1(ctx, cursor)
	if err != nil {
		return "", err
	}

	return marshalResult(map[string]any{"documents": docs, "count": len(docs)})
}

func mongoFindOneV1(ctx context.Context, client *mongo.Client, database, collection string, queryMap map[string]json.RawMessage) (string, error) {
	if database == "" || collection == "" {
		return "", fmt.Errorf("findOne 操作需要 database 和 collection 参数")
	}

	filter, err := toBSONDocV1(queryMap, "filter")
	if err != nil {
		return "", fmt.Errorf("解析 filter 失败: %w", err)
	}
	if filter == nil {
		filter = bson.D{}
	}

	findOneOpts := options.FindOne()
	if projRaw, ok := queryMap["projection"]; ok {
		var projDoc bson.D
		if err := bson.UnmarshalExtJSON(projRaw, false, &projDoc); err != nil {
			return "", fmt.Errorf("解析 projection 失败: %w", err)
		}
		findOneOpts.SetProjection(projDoc)
	}

	coll := client.Database(database).Collection(collection)
	var doc bson.D
	err = coll.FindOne(ctx, filter, findOneOpts).Decode(&doc)
	if err != nil {
		if err == mongo.ErrNoDocuments {
			return marshalResult(map[string]any{"document": nil})
		}
		return "", fmt.Errorf("MongoDB findOne 失败: %w", err)
	}

	jsonDoc, err := bsonDocToJSONV1(doc)
	if err != nil {
		return "", err
	}

	return marshalResult(map[string]any{"document": jsonDoc})
}

func mongoInsertOneV1(ctx context.Context, client *mongo.Client, database, collection string, queryMap map[string]json.RawMessage) (string, error) {
	if database == "" || collection == "" {
		return "", fmt.Errorf("insertOne 操作需要 database 和 collection 参数")
	}

	docRaw, ok := queryMap["document"]
	if !ok {
		return "", fmt.Errorf("insertOne 操作需要 document 参数")
	}
	var doc bson.D
	if err := bson.UnmarshalExtJSON(docRaw, false, &doc); err != nil {
		return "", fmt.Errorf("解析 document 失败: %w", err)
	}

	coll := client.Database(database).Collection(collection)
	result, err := coll.InsertOne(ctx, doc)
	if err != nil {
		return "", fmt.Errorf("MongoDB insertOne 失败: %w", err)
	}

	return marshalResult(map[string]any{"insertedId": fmt.Sprint(result.InsertedID)})
}

func mongoInsertManyV1(ctx context.Context, client *mongo.Client, database, collection string, queryMap map[string]json.RawMessage) (string, error) {
	if database == "" || collection == "" {
		return "", fmt.Errorf("insertMany 操作需要 database 和 collection 参数")
	}

	docsRaw, ok := queryMap["documents"]
	if !ok {
		return "", fmt.Errorf("insertMany 操作需要 documents 参数")
	}

	var rawDocs []json.RawMessage
	if err := json.Unmarshal(docsRaw, &rawDocs); err != nil {
		return "", fmt.Errorf("解析 documents 数组失败: %w", err)
	}

	docs := make([]any, len(rawDocs))
	for i, raw := range rawDocs {
		var doc bson.D
		if err := bson.UnmarshalExtJSON(raw, false, &doc); err != nil {
			return "", fmt.Errorf("解析 documents[%d] 失败: %w", i, err)
		}
		docs[i] = doc
	}

	coll := client.Database(database).Collection(collection)
	result, err := coll.InsertMany(ctx, docs)
	if err != nil {
		return "", fmt.Errorf("MongoDB insertMany 失败: %w", err)
	}

	ids := make([]string, len(result.InsertedIDs))
	for i, id := range result.InsertedIDs {
		ids[i] = fmt.Sprint(id)
	}

	return marshalResult(map[string]any{"insertedIds": ids, "count": len(ids)})
}

func mongoUpdateOneV1(ctx context.Context, client *mongo.Client, database, collection string, queryMap map[string]json.RawMessage) (string, error) {
	if database == "" || collection == "" {
		return "", fmt.Errorf("updateOne 操作需要 database 和 collection 参数")
	}

	filter, err := toBSONDocV1(queryMap, "filter")
	if err != nil {
		return "", fmt.Errorf("解析 filter 失败: %w", err)
	}
	if filter == nil {
		filter = bson.D{}
	}

	update, err := toBSONDocV1(queryMap, "update")
	if err != nil {
		return "", fmt.Errorf("解析 update 失败: %w", err)
	}
	if update == nil {
		return "", fmt.Errorf("updateOne 操作需要 update 参数")
	}

	coll := client.Database(database).Collection(collection)
	result, err := coll.UpdateOne(ctx, filter, update)
	if err != nil {
		return "", fmt.Errorf("MongoDB updateOne 失败: %w", err)
	}

	return marshalResult(map[string]any{
		"matchedCount":  result.MatchedCount,
		"modifiedCount": result.ModifiedCount,
	})
}

func mongoUpdateManyV1(ctx context.Context, client *mongo.Client, database, collection string, queryMap map[string]json.RawMessage) (string, error) {
	if database == "" || collection == "" {
		return "", fmt.Errorf("updateMany 操作需要 database 和 collection 参数")
	}

	filter, err := toBSONDocV1(queryMap, "filter")
	if err != nil {
		return "", fmt.Errorf("解析 filter 失败: %w", err)
	}
	if filter == nil {
		filter = bson.D{}
	}

	update, err := toBSONDocV1(queryMap, "update")
	if err != nil {
		return "", fmt.Errorf("解析 update 失败: %w", err)
	}
	if update == nil {
		return "", fmt.Errorf("updateMany 操作需要 update 参数")
	}

	coll := client.Database(database).Collection(collection)
	result, err := coll.UpdateMany(ctx, filter, update)
	if err != nil {
		return "", fmt.Errorf("MongoDB updateMany 失败: %w", err)
	}

	return marshalResult(map[string]any{
		"matchedCount":  result.MatchedCount,
		"modifiedCount": result.ModifiedCount,
	})
}

func mongoDeleteOneV1(ctx context.Context, client *mongo.Client, database, collection string, queryMap map[string]json.RawMessage) (string, error) {
	if database == "" || collection == "" {
		return "", fmt.Errorf("deleteOne 操作需要 database 和 collection 参数")
	}

	filter, err := toBSONDocV1(queryMap, "filter")
	if err != nil {
		return "", fmt.Errorf("解析 filter 失败: %w", err)
	}
	if filter == nil {
		filter = bson.D{}
	}

	coll := client.Database(database).Collection(collection)
	result, err := coll.DeleteOne(ctx, filter)
	if err != nil {
		return "", fmt.Errorf("MongoDB deleteOne 失败: %w", err)
	}

	return marshalResult(map[string]any{"deletedCount": result.DeletedCount})
}

func mongoDeleteManyV1(ctx context.Context, client *mongo.Client, database, collection string, queryMap map[string]json.RawMessage) (string, error) {
	if database == "" || collection == "" {
		return "", fmt.Errorf("deleteMany 操作需要 database 和 collection 参数")
	}

	filter, err := toBSONDocV1(queryMap, "filter")
	if err != nil {
		return "", fmt.Errorf("解析 filter 失败: %w", err)
	}
	if filter == nil {
		filter = bson.D{}
	}

	coll := client.Database(database).Collection(collection)
	result, err := coll.DeleteMany(ctx, filter)
	if err != nil {
		return "", fmt.Errorf("MongoDB deleteMany 失败: %w", err)
	}

	return marshalResult(map[string]any{"deletedCount": result.DeletedCount})
}

func mongoAggregateV1(ctx context.Context, client *mongo.Client, database, collection string, queryMap map[string]json.RawMessage) (string, error) {
	if database == "" || collection == "" {
		return "", fmt.Errorf("aggregate 操作需要 database 和 collection 参数")
	}

	pipelineRaw, ok := queryMap["pipeline"]
	if !ok {
		return "", fmt.Errorf("aggregate 操作需要 pipeline 参数")
	}

	var rawStages []json.RawMessage
	if err := json.Unmarshal(pipelineRaw, &rawStages); err != nil {
		return "", fmt.Errorf("解析 pipeline 数组失败: %w", err)
	}

	pipeline := make(bson.A, len(rawStages))
	for i, raw := range rawStages {
		var stage bson.D
		if err := bson.UnmarshalExtJSON(raw, false, &stage); err != nil {
			return "", fmt.Errorf("解析 pipeline[%d] 失败: %w", i, err)
		}
		pipeline[i] = stage
	}

	coll := client.Database(database).Collection(collection)
	cursor, err := coll.Aggregate(ctx, pipeline)
	if err != nil {
		return "", fmt.Errorf("MongoDB aggregate 失败: %w", err)
	}
	defer func() {
		if err := cursor.Close(ctx); err != nil {
			logger.Default().Warn("close MongoDB cursor", zap.Error(err))
		}
	}()

	docs, err := cursorToJSONV1(ctx, cursor)
	if err != nil {
		return "", err
	}

	return marshalResult(map[string]any{"documents": docs, "count": len(docs)})
}

func mongoCountDocumentsV1(ctx context.Context, client *mongo.Client, database, collection string, queryMap map[string]json.RawMessage) (string, error) {
	if database == "" || collection == "" {
		return "", fmt.Errorf("countDocuments 操作需要 database 和 collection 参数")
	}

	filter, err := toBSONDocV1(queryMap, "filter")
	if err != nil {
		return "", fmt.Errorf("解析 filter 失败: %w", err)
	}
	if filter == nil {
		filter = bson.D{}
	}

	coll := client.Database(database).Collection(collection)
	count, err := coll.CountDocuments(ctx, filter)
	if err != nil {
		return "", fmt.Errorf("MongoDB countDocuments 失败: %w", err)
	}

	return marshalResult(map[string]any{"count": count})
}

func toBSONDocV1(queryMap map[string]json.RawMessage, key string) (bson.D, error) {
	raw, ok := queryMap[key]
	if !ok {
		return nil, nil
	}
	var doc bson.D
	if err := bson.UnmarshalExtJSON(raw, false, &doc); err != nil {
		return nil, err
	}
	return doc, nil
}

func cursorToJSONV1(ctx context.Context, cursor *mongo.Cursor) ([]json.RawMessage, error) {
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

func bsonDocToJSONV1(doc bson.D) (json.RawMessage, error) {
	jsonBytes, err := bson.MarshalExtJSON(doc, false, false)
	if err != nil {
		return nil, fmt.Errorf("转换文档为 JSON 失败: %w", err)
	}
	return jsonBytes, nil
}
