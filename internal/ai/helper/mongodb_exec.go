package helper

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

	"github.com/opskat/opskat/internal/connpool"
)

// mongoDBExecutor 是 MongoDB 操作与具体驱动（v1/v2）之间的接缝。
//
// 接口用 JSON/原始类型做货币（filter/sort/projection/update/document/pipeline 都以
// extended JSON 形式传入，文档以 []json.RawMessage 返回），两个驱动各自在边界内
// 转成自己的 bson 包。这样 12 个操作的全部**逻辑**（参数校验、默认值、结果形状）
// 只实现一次，v1/v2 各只剩一层薄适配器——把"旧版镜像分叉"从运行时风险变成
// 编译期保证：新增操作只要接口加方法，两个适配器必须都实现，漏一个就不编译。
type mongoDBExecutor interface {
	// ListDatabases 列出所有数据库名。
	ListDatabases(ctx context.Context) ([]string, error)
	// ListCollections 列出指定数据库的所有集合名。
	ListCollections(ctx context.Context, database string) ([]string, error)
	// Find 查询文档，返回每篇文档的 extended JSON。filter 为 nil 时表示不过滤。
	// limit 由共享逻辑保证 >0（默认 100）。
	Find(ctx context.Context, database, collection string, filter, sort, projection json.RawMessage, limit, skip int64) ([]json.RawMessage, error)
	// FindOne 返回单篇文档的 extended JSON；无匹配时返回 (nil, false, nil)。
	FindOne(ctx context.Context, database, collection string, filter, projection json.RawMessage) (json.RawMessage, bool, error)
	// InsertOne 返回插入 ID 的字符串形式。
	InsertOne(ctx context.Context, database, collection string, document json.RawMessage) (string, error)
	// InsertMany 返回各插入 ID 的字符串形式。
	InsertMany(ctx context.Context, database, collection string, documents []json.RawMessage) ([]string, error)
	// UpdateOne / UpdateMany 返回 (matchedCount, modifiedCount)。
	UpdateOne(ctx context.Context, database, collection string, filter, update json.RawMessage) (int64, int64, error)
	UpdateMany(ctx context.Context, database, collection string, filter, update json.RawMessage) (int64, int64, error)
	// DeleteOne / DeleteMany 返回删除的文档数。
	DeleteOne(ctx context.Context, database, collection string, filter json.RawMessage) (int64, error)
	DeleteMany(ctx context.Context, database, collection string, filter json.RawMessage) (int64, error)
	// Aggregate 按 pipeline 聚合，返回文档的 extended JSON。pipeline 为 extended JSON 数组。
	Aggregate(ctx context.Context, database, collection string, pipeline json.RawMessage) ([]json.RawMessage, error)
	// CountDocuments 返回匹配 filter 的文档数。
	CountDocuments(ctx context.Context, database, collection string, filter json.RawMessage) (int64, error)
}

// executorFor 按连接是 v1（旧版兼容）还是 v2（默认）选择驱动适配器。
func executorFor(client *connpool.MongoClientCloser) mongoDBExecutor {
	if client.Legacy {
		return &mongoExecutorV1{client: client.V1}
	}
	return &mongoExecutorV2{client: client.V2}
}

// mongoOpSpec 描述一个 mongo 操作：是否要求 collection / database 参数，以及怎么执行。
//
// mongoOps 是 ParseMongoCommand（internal/ai/helper/mongo_command.go）、
// resolveMongoCommand（internal/ai/helper/mongo_exec.go）与 ExecuteMongoDB 共用的
// **唯一**操作列表，v1/v2 两个驱动都从这一张表分发。过去 v1 侧另有一份镜像
// mongodb_helper_legacy.go 的 mongoOpsV1，新增操作只改一边就会让 legacy 资产与
// 默认资产悄悄跑偏；现在不存在第二张表，跑偏在结构上不可能发生。
//
// needsDatabase 供 resolveMongoCommand 在权限检查之前判断"这条命令没有可用的
// database，必然执行失败"：下方每个 mongoXxx 函数都会在 database 为空时报错，
// 唯独 listDatabases 不需要 database。把这个判断挪到这里而不是在 resolveMongoCommand
// 里另建一份操作名单，理由与 needsCollection 完全一致。
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
	exec            func(ctx context.Context, ex mongoDBExecutor, database, collection string, queryMap map[string]json.RawMessage) (string, error)
}

// ExecuteMongoDB 执行 MongoDB 操作并返回 JSON 结果
func ExecuteMongoDB(ctx context.Context, client *connpool.MongoClientCloser, database, collection, operation, query string) (string, error) {
	// 解析 query JSON
	queryMap, err := parseQueryMap(query)
	if err != nil {
		return "", fmt.Errorf("无效的查询参数: %w", err)
	}

	spec, ok := mongoOps[operation]
	if !ok {
		return "", fmt.Errorf("不支持的 MongoDB 操作: %s", operation)
	}
	return spec.exec(ctx, executorFor(client), database, collection, queryMap)
}

func mongoListDatabases(ctx context.Context, ex mongoDBExecutor, _, _ string, _ map[string]json.RawMessage) (string, error) {
	names, err := ex.ListDatabases(ctx)
	if err != nil {
		return "", err
	}
	return marshalResult(map[string]any{"databases": names, "count": len(names)})
}

func mongoListCollections(ctx context.Context, ex mongoDBExecutor, database, _ string, _ map[string]json.RawMessage) (string, error) {
	if database == "" {
		return "", fmt.Errorf("database 参数不能为空")
	}
	names, err := ex.ListCollections(ctx, database)
	if err != nil {
		return "", err
	}
	return marshalResult(map[string]any{"collections": names, "count": len(names)})
}

func mongoFind(ctx context.Context, ex mongoDBExecutor, database, collection string, queryMap map[string]json.RawMessage) (string, error) {
	if database == "" || collection == "" {
		return "", fmt.Errorf("find 操作需要 database 和 collection 参数")
	}

	limit := int64(100)
	if limitRaw, ok := queryMap["limit"]; ok {
		if l, err := jsonInt64(limitRaw); err == nil && l > 0 {
			limit = l
		}
	}
	var skip int64
	if skipRaw, ok := queryMap["skip"]; ok {
		if s, err := jsonInt64(skipRaw); err == nil && s >= 0 {
			skip = s
		}
	}

	docs, err := ex.Find(ctx, database, collection, rawArg(queryMap, "filter"), rawArg(queryMap, "sort"), rawArg(queryMap, "projection"), limit, skip)
	if err != nil {
		return "", err
	}
	return marshalResult(map[string]any{"documents": docs, "count": len(docs)})
}

func mongoFindOne(ctx context.Context, ex mongoDBExecutor, database, collection string, queryMap map[string]json.RawMessage) (string, error) {
	if database == "" || collection == "" {
		return "", fmt.Errorf("findOne 操作需要 database 和 collection 参数")
	}

	doc, found, err := ex.FindOne(ctx, database, collection, rawArg(queryMap, "filter"), rawArg(queryMap, "projection"))
	if err != nil {
		return "", err
	}
	if !found {
		return marshalResult(map[string]any{"document": nil})
	}
	return marshalResult(map[string]any{"document": doc})
}

func mongoInsertOne(ctx context.Context, ex mongoDBExecutor, database, collection string, queryMap map[string]json.RawMessage) (string, error) {
	if database == "" || collection == "" {
		return "", fmt.Errorf("insertOne 操作需要 database 和 collection 参数")
	}

	docRaw, ok := queryMap["document"]
	if !ok {
		return "", fmt.Errorf("insertOne 操作需要 document 参数")
	}
	id, err := ex.InsertOne(ctx, database, collection, docRaw)
	if err != nil {
		return "", err
	}
	return marshalResult(map[string]any{"insertedId": id})
}

func mongoInsertMany(ctx context.Context, ex mongoDBExecutor, database, collection string, queryMap map[string]json.RawMessage) (string, error) {
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

	ids, err := ex.InsertMany(ctx, database, collection, rawDocs)
	if err != nil {
		return "", err
	}
	return marshalResult(map[string]any{"insertedIds": ids, "count": len(ids)})
}

func mongoUpdateOne(ctx context.Context, ex mongoDBExecutor, database, collection string, queryMap map[string]json.RawMessage) (string, error) {
	if database == "" || collection == "" {
		return "", fmt.Errorf("updateOne 操作需要 database 和 collection 参数")
	}

	update := rawArg(queryMap, "update")
	if update == nil || isJSONNull(update) {
		return "", fmt.Errorf("updateOne 操作需要 update 参数")
	}
	matched, modified, err := ex.UpdateOne(ctx, database, collection, rawArg(queryMap, "filter"), update)
	if err != nil {
		return "", err
	}
	return marshalResult(map[string]any{"matchedCount": matched, "modifiedCount": modified})
}

func mongoUpdateMany(ctx context.Context, ex mongoDBExecutor, database, collection string, queryMap map[string]json.RawMessage) (string, error) {
	if database == "" || collection == "" {
		return "", fmt.Errorf("updateMany 操作需要 database 和 collection 参数")
	}

	update := rawArg(queryMap, "update")
	if update == nil || isJSONNull(update) {
		return "", fmt.Errorf("updateMany 操作需要 update 参数")
	}
	matched, modified, err := ex.UpdateMany(ctx, database, collection, rawArg(queryMap, "filter"), update)
	if err != nil {
		return "", err
	}
	return marshalResult(map[string]any{"matchedCount": matched, "modifiedCount": modified})
}

func mongoDeleteOne(ctx context.Context, ex mongoDBExecutor, database, collection string, queryMap map[string]json.RawMessage) (string, error) {
	if database == "" || collection == "" {
		return "", fmt.Errorf("deleteOne 操作需要 database 和 collection 参数")
	}

	deleted, err := ex.DeleteOne(ctx, database, collection, rawArg(queryMap, "filter"))
	if err != nil {
		return "", err
	}
	return marshalResult(map[string]any{"deletedCount": deleted})
}

func mongoDeleteMany(ctx context.Context, ex mongoDBExecutor, database, collection string, queryMap map[string]json.RawMessage) (string, error) {
	if database == "" || collection == "" {
		return "", fmt.Errorf("deleteMany 操作需要 database 和 collection 参数")
	}

	deleted, err := ex.DeleteMany(ctx, database, collection, rawArg(queryMap, "filter"))
	if err != nil {
		return "", err
	}
	return marshalResult(map[string]any{"deletedCount": deleted})
}

func mongoAggregate(ctx context.Context, ex mongoDBExecutor, database, collection string, queryMap map[string]json.RawMessage) (string, error) {
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

	docs, err := ex.Aggregate(ctx, database, collection, pipelineRaw)
	if err != nil {
		return "", err
	}
	return marshalResult(map[string]any{"documents": docs, "count": len(docs)})
}

func mongoCountDocuments(ctx context.Context, ex mongoDBExecutor, database, collection string, queryMap map[string]json.RawMessage) (string, error) {
	if database == "" || collection == "" {
		return "", fmt.Errorf("countDocuments 操作需要 database 和 collection 参数")
	}

	count, err := ex.CountDocuments(ctx, database, collection, rawArg(queryMap, "filter"))
	if err != nil {
		return "", err
	}
	return marshalResult(map[string]any{"count": count})
}

// isJSONNull 判断原始 JSON 是否为字面量 null（镜像旧 toBSONDoc 把 JSON null 解析成
// nil 后触发"需要 update 参数"的行为）。
func isJSONNull(raw json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}

// rawArg 从 queryMap 提取指定 key 的原始 JSON；key 缺失返回 nil（不校验内容，
// 校验与 bson 解析在驱动适配器里做）。
func rawArg(queryMap map[string]json.RawMessage, key string) json.RawMessage {
	raw, ok := queryMap[key]
	if !ok {
		return nil
	}
	return raw
}

// jsonInt64 把原始 JSON 解析为 int64。
func jsonInt64(raw json.RawMessage) (int64, error) {
	var n int64
	if err := json.Unmarshal(raw, &n); err != nil {
		return 0, err
	}
	return n, nil
}
