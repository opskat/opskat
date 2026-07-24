package helper

import (
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/cago-frame/cago/pkg/logger"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.uber.org/zap"

	"github.com/opskat/opskat/internal/connpool"
	"github.com/opskat/opskat/internal/model/entity/asset_entity"
	"github.com/opskat/opskat/internal/service/credential_resolver"
)

// --- MongoDB 连接缓存 ---

type mongoDBCacheKeyType struct{}

// MongoDBClientCache 在同一次 AI Send 中复用 MongoDB 连接
type MongoDBClientCache = ConnCache[*connpool.MongoClientCloser]

// NewMongoDBClientCache 创建 MongoDB 连接缓存
func NewMongoDBClientCache() *MongoDBClientCache {
	return NewConnCache[*connpool.MongoClientCloser]("MongoDB")
}

// WithMongoDBCache 将 MongoDB 缓存注入 context
func WithMongoDBCache(ctx context.Context, cache *MongoDBClientCache) context.Context {
	return context.WithValue(ctx, mongoDBCacheKeyType{}, cache)
}

func getMongoDBCache(ctx context.Context) *MongoDBClientCache {
	if cache, ok := ctx.Value(mongoDBCacheKeyType{}).(*MongoDBClientCache); ok {
		return cache
	}
	return nil
}

func getOrDialMongoDB(ctx context.Context, asset *asset_entity.Asset) (*mongo.Client, io.Closer, error) {
	dialFn := func() (*connpool.MongoClientCloser, io.Closer, error) {
		cfg, err := asset.GetMongoDBConfig()
		if err != nil {
			return nil, nil, fmt.Errorf("failed to get MongoDB config: %w", err)
		}
		password, err := credential_resolver.Default().ResolveMongoDBPassword(ctx, cfg)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to resolve credentials: %w", err)
		}
		cfg.Proxy = credential_resolver.Default().DecryptProxyPassword(cfg.Proxy)
		client, closer, err := connpool.DialMongoDB(ctx, asset, cfg, password, getSSHPool(ctx))
		if err != nil {
			return nil, nil, err
		}
		return &connpool.MongoClientCloser{Client: client}, closer, nil
	}
	if cache := getMongoDBCache(ctx); cache != nil {
		wrapper, closer, err := cache.GetOrDial(asset.ID, dialFn)
		if err != nil {
			return nil, nil, err
		}
		return wrapper.Client, closer, nil
	}
	wrapper, closer, err := dialFn()
	if err != nil {
		return nil, nil, err
	}
	return wrapper.Client, closer, nil
}

// mongoOpSpec 描述一个 mongo 操作：是否要求 collection / database 参数，以及怎么执行。
//
// mongoOps 是 ParseMongoCommand（internal/ai/helper/mongo_command.go）与
// ExecuteMongoDB 共用的唯一操作列表：两者过去各维护一份同样的 12 个操作名
// （一份 map[string]bool 判断要不要 collection，一份 switch 分发执行），新增
// 操作只改一处就会悄悄跑偏。现在两边都读这同一个 map，跑偏在结构上不可能
// 发生——这里少一个 entry，ParseMongoCommand 和 ExecuteMongoDB 会对同一个
// 操作名同时拒绝，不存在"一边认得一边不认得"的中间态。
//
// needsDatabase 供 resolveMongoCommand（internal/ai/helper/mongo_exec.go）在权限检查
// 之前判断"这条命令没有可用的 database，必然执行失败"：本文件下方每个 mongoXxx 函数
// 都会在 database 为空时报错，唯独 listDatabases 不需要 database。把这个判断挪到这里
// 而不是在 resolveMongoCommand 里另建一份操作名单，理由与 needsCollection 完全一致。
var mongoOps = map[string]mongoOpSpec{
	"find":            {needsCollection: true, needsDatabase: true, exec: mongoFind},
	"findOne":         {needsCollection: true, needsDatabase: true, exec: mongoFindOne},
	"insertOne":       {needsCollection: true, needsDatabase: true, exec: mongoInsertOne},
	"insertMany":      {needsCollection: true, needsDatabase: true, exec: mongoInsertMany},
	"updateOne":       {needsCollection: true, needsDatabase: true, exec: mongoUpdateOne},
	"updateMany":      {needsCollection: true, needsDatabase: true, exec: mongoUpdateMany},
	"deleteOne":       {needsCollection: true, needsDatabase: true, exec: mongoDeleteOne},
	"deleteMany":      {needsCollection: true, needsDatabase: true, exec: mongoDeleteMany},
	"aggregate":       {needsCollection: true, needsDatabase: true, exec: mongoAggregate},
	"countDocuments":  {needsCollection: true, needsDatabase: true, exec: mongoCountDocuments},
	"listDatabases":   {needsCollection: false, needsDatabase: false, exec: mongoListDatabases},
	"listCollections": {needsCollection: false, needsDatabase: true, exec: mongoListCollections},
}

type mongoOpSpec struct {
	needsCollection bool
	needsDatabase   bool
	exec            func(ctx context.Context, client *mongo.Client, database, collection string, queryMap map[string]json.RawMessage) (string, error)
}

// ExecuteMongoDB 执行 MongoDB 操作并返回 JSON 结果
func ExecuteMongoDB(ctx context.Context, client *mongo.Client, database, collection, operation, query string) (string, error) {
	// 解析 query JSON
	queryMap, err := parseQueryMap(query)
	if err != nil {
		return "", fmt.Errorf("无效的查询参数: %w", err)
	}

	spec, ok := mongoOps[operation]
	if !ok {
		return "", fmt.Errorf("不支持的 MongoDB 操作: %s", operation)
	}
	return spec.exec(ctx, client, database, collection, queryMap)
}

func mongoListDatabases(ctx context.Context, client *mongo.Client, _, _ string, _ map[string]json.RawMessage) (string, error) {
	names, err := ListMongoDatabases(ctx, client)
	if err != nil {
		return "", err
	}
	return marshalResult(map[string]any{"databases": names, "count": len(names)})
}

func mongoListCollections(ctx context.Context, client *mongo.Client, database, _ string, _ map[string]json.RawMessage) (string, error) {
	if database == "" {
		return "", fmt.Errorf("database 参数不能为空")
	}
	names, err := ListMongoCollections(ctx, client, database)
	if err != nil {
		return "", err
	}
	return marshalResult(map[string]any{"collections": names, "count": len(names)})
}

// ListMongoDatabases 列出所有数据库名称
func ListMongoDatabases(ctx context.Context, client *mongo.Client) ([]string, error) {
	names, err := client.ListDatabaseNames(ctx, bson.D{})
	if err != nil {
		return nil, fmt.Errorf("列出数据库失败: %w", err)
	}
	return names, nil
}

// ListMongoCollections 列出指定数据库的所有集合名称
func ListMongoCollections(ctx context.Context, client *mongo.Client, database string) ([]string, error) {
	names, err := client.Database(database).ListCollectionNames(ctx, bson.D{})
	if err != nil {
		return nil, fmt.Errorf("列出集合失败: %w", err)
	}
	return names, nil
}

// --- 内部操作实现 ---

func mongoFind(ctx context.Context, client *mongo.Client, database, collection string, queryMap map[string]json.RawMessage) (string, error) {
	if database == "" || collection == "" {
		return "", fmt.Errorf("find 操作需要 database 和 collection 参数")
	}

	filter, err := toBSONDoc(queryMap, "filter")
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

	docs, err := cursorToJSON(ctx, cursor)
	if err != nil {
		return "", err
	}

	return marshalResult(map[string]any{"documents": docs, "count": len(docs)})
}

func mongoFindOne(ctx context.Context, client *mongo.Client, database, collection string, queryMap map[string]json.RawMessage) (string, error) {
	if database == "" || collection == "" {
		return "", fmt.Errorf("findOne 操作需要 database 和 collection 参数")
	}

	filter, err := toBSONDoc(queryMap, "filter")
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

	jsonDoc, err := bsonDocToJSON(doc)
	if err != nil {
		return "", err
	}

	return marshalResult(map[string]any{"document": jsonDoc})
}

func mongoInsertOne(ctx context.Context, client *mongo.Client, database, collection string, queryMap map[string]json.RawMessage) (string, error) {
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

func mongoInsertMany(ctx context.Context, client *mongo.Client, database, collection string, queryMap map[string]json.RawMessage) (string, error) {
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

func mongoUpdateOne(ctx context.Context, client *mongo.Client, database, collection string, queryMap map[string]json.RawMessage) (string, error) {
	if database == "" || collection == "" {
		return "", fmt.Errorf("updateOne 操作需要 database 和 collection 参数")
	}

	filter, err := toBSONDoc(queryMap, "filter")
	if err != nil {
		return "", fmt.Errorf("解析 filter 失败: %w", err)
	}
	if filter == nil {
		filter = bson.D{}
	}

	update, err := toBSONDoc(queryMap, "update")
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

func mongoUpdateMany(ctx context.Context, client *mongo.Client, database, collection string, queryMap map[string]json.RawMessage) (string, error) {
	if database == "" || collection == "" {
		return "", fmt.Errorf("updateMany 操作需要 database 和 collection 参数")
	}

	filter, err := toBSONDoc(queryMap, "filter")
	if err != nil {
		return "", fmt.Errorf("解析 filter 失败: %w", err)
	}
	if filter == nil {
		filter = bson.D{}
	}

	update, err := toBSONDoc(queryMap, "update")
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

func mongoDeleteOne(ctx context.Context, client *mongo.Client, database, collection string, queryMap map[string]json.RawMessage) (string, error) {
	if database == "" || collection == "" {
		return "", fmt.Errorf("deleteOne 操作需要 database 和 collection 参数")
	}

	filter, err := toBSONDoc(queryMap, "filter")
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

func mongoDeleteMany(ctx context.Context, client *mongo.Client, database, collection string, queryMap map[string]json.RawMessage) (string, error) {
	if database == "" || collection == "" {
		return "", fmt.Errorf("deleteMany 操作需要 database 和 collection 参数")
	}

	filter, err := toBSONDoc(queryMap, "filter")
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

func mongoAggregate(ctx context.Context, client *mongo.Client, database, collection string, queryMap map[string]json.RawMessage) (string, error) {
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

	docs, err := cursorToJSON(ctx, cursor)
	if err != nil {
		return "", err
	}

	return marshalResult(map[string]any{"documents": docs, "count": len(docs)})
}

func mongoCountDocuments(ctx context.Context, client *mongo.Client, database, collection string, queryMap map[string]json.RawMessage) (string, error) {
	if database == "" || collection == "" {
		return "", fmt.Errorf("countDocuments 操作需要 database 和 collection 参数")
	}

	filter, err := toBSONDoc(queryMap, "filter")
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

// --- 辅助函数 ---

// parseQueryMap 将 query JSON 字符串解析为 map[string]json.RawMessage
func parseQueryMap(query string) (map[string]json.RawMessage, error) {
	if query == "" || query == "{}" {
		return make(map[string]json.RawMessage), nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal([]byte(query), &m); err != nil {
		return nil, err
	}
	return m, nil
}

// toBSONDoc 从 queryMap 中提取指定 key 的值，转换为 bson.D
// 如果 key 不存在，返回 nil, nil
func toBSONDoc(queryMap map[string]json.RawMessage, key string) (bson.D, error) {
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

// marshalResult 将结果 map 序列化为 JSON 字符串
func marshalResult(result map[string]any) (string, error) {
	data, err := json.Marshal(result)
	if err != nil {
		logger.Default().Error("marshal MongoDB result", zap.Error(err))
		return "", fmt.Errorf("序列化结果失败: %w", err)
	}
	return string(data), nil
}
