package helper

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/opskat/opskat/internal/connpool"
)

// fakeMongoExecutor 记录驱动调用参数并按预设返回，用于把 12 个共享操作的
// 参数路由与结果形状钉在接口层——v1/v2 两个真适配器都是这一层接口的薄实现，
// 逻辑只测这一份。
type fakeMongoExecutor struct {
	db, collection             string
	filter, sort, projection   json.RawMessage
	update, pipeline, document json.RawMessage
	limit, skip                int64
	documents                  []json.RawMessage

	docs       []json.RawMessage
	doc        json.RawMessage
	found      bool
	insertedID string
	ids        []string
	matched    int64
	modified   int64
	deleted    int64
	count      int64
	err        error
}

func (f *fakeMongoExecutor) ListDatabases(context.Context) ([]string, error) {
	if f.err != nil {
		return nil, f.err
	}
	return []string{"admin", "test"}, nil
}

func (f *fakeMongoExecutor) ListCollections(_ context.Context, database string) ([]string, error) {
	f.db = database
	if f.err != nil {
		return nil, f.err
	}
	return []string{"items"}, nil
}

func (f *fakeMongoExecutor) Find(_ context.Context, db, collection string, filter, sort, projection json.RawMessage, limit, skip int64) ([]json.RawMessage, error) {
	f.db, f.collection = db, collection
	f.filter, f.sort, f.projection = filter, sort, projection
	f.limit, f.skip = limit, skip
	return f.docs, f.err
}

func (f *fakeMongoExecutor) FindOne(_ context.Context, db, collection string, filter, projection json.RawMessage) (json.RawMessage, bool, error) {
	f.db, f.collection = db, collection
	f.filter, f.projection = filter, projection
	return f.doc, f.found, f.err
}

func (f *fakeMongoExecutor) InsertOne(_ context.Context, db, collection string, document json.RawMessage) (string, error) {
	f.db, f.collection, f.document = db, collection, document
	return f.insertedID, f.err
}

func (f *fakeMongoExecutor) InsertMany(_ context.Context, db, collection string, documents []json.RawMessage) ([]string, error) {
	f.db, f.collection, f.documents = db, collection, documents
	return f.ids, f.err
}

func (f *fakeMongoExecutor) UpdateOne(_ context.Context, db, collection string, filter, update json.RawMessage) (int64, int64, error) {
	f.db, f.collection, f.filter, f.update = db, collection, filter, update
	return f.matched, f.modified, f.err
}

func (f *fakeMongoExecutor) UpdateMany(_ context.Context, db, collection string, filter, update json.RawMessage) (int64, int64, error) {
	f.db, f.collection, f.filter, f.update = db, collection, filter, update
	return f.matched, f.modified, f.err
}

func (f *fakeMongoExecutor) DeleteOne(_ context.Context, db, collection string, filter json.RawMessage) (int64, error) {
	f.db, f.collection, f.filter = db, collection, filter
	return f.deleted, f.err
}

func (f *fakeMongoExecutor) DeleteMany(_ context.Context, db, collection string, filter json.RawMessage) (int64, error) {
	f.db, f.collection, f.filter = db, collection, filter
	return f.deleted, f.err
}

func (f *fakeMongoExecutor) Aggregate(_ context.Context, db, collection string, pipeline json.RawMessage) ([]json.RawMessage, error) {
	f.db, f.collection, f.pipeline = db, collection, pipeline
	return f.docs, f.err
}

func (f *fakeMongoExecutor) CountDocuments(_ context.Context, db, collection string, filter json.RawMessage) (int64, error) {
	f.db, f.collection, f.filter = db, collection, filter
	return f.count, f.err
}

// runOp 走与 ExecuteMongoDB 完全相同的分发（mongoOps 表），让行为测试同时
// 钉住"表里每个操作都有可执行的实现"。
func runOp(t *testing.T, op, db, collection string, query map[string]json.RawMessage, fake *fakeMongoExecutor) (string, error) {
	t.Helper()
	spec, ok := mongoOps[op]
	if !ok || spec.exec == nil {
		t.Fatalf("mongoOps[%q] has no exec", op)
	}
	return spec.exec(context.Background(), fake, db, collection, query)
}

func qm(t *testing.T, raw string) map[string]json.RawMessage {
	t.Helper()
	m, err := parseQueryMap(raw)
	require.NoError(t, err)
	return m
}

func TestMongoFindShared(t *testing.T) {
	fake := &fakeMongoExecutor{docs: []json.RawMessage{json.RawMessage(`{"a":1}`), json.RawMessage(`{"a":2}`)}}
	out, err := runOp(t, "find", "db1", "c1", qm(t, `{"filter":{"x":1},"sort":{"x":-1},"projection":{"x":1},"limit":50,"skip":10}`), fake)
	require.NoError(t, err)
	assert.JSONEq(t, `{"documents":[{"a":1},{"a":2}],"count":2}`, out)
	assert.Equal(t, "db1", fake.db)
	assert.Equal(t, "c1", fake.collection)
	assert.JSONEq(t, `{"x":1}`, string(fake.filter))
	assert.JSONEq(t, `{"x":-1}`, string(fake.sort))
	assert.JSONEq(t, `{"x":1}`, string(fake.projection))
	assert.Equal(t, int64(50), fake.limit)
	assert.Equal(t, int64(10), fake.skip)
}

func TestMongoFindSharedDefaults(t *testing.T) {
	fake := &fakeMongoExecutor{}
	_, err := runOp(t, "find", "db1", "c1", qm(t, `{}`), fake)
	require.NoError(t, err)
	assert.Nil(t, fake.filter)
	assert.Nil(t, fake.sort)
	assert.Nil(t, fake.projection)
	// 未给 limit 时默认 100；skip 默认 0。
	assert.Equal(t, int64(100), fake.limit)
	assert.Equal(t, int64(0), fake.skip)

	// 非法 limit 忽略，仍用默认 100。
	fake2 := &fakeMongoExecutor{}
	_, err = runOp(t, "find", "db1", "c1", qm(t, `{"limit":"abc"}`), fake2)
	require.NoError(t, err)
	assert.Equal(t, int64(100), fake2.limit)
}

func TestMongoFindSharedRequiresDBAndCollection(t *testing.T) {
	_, err := runOp(t, "find", "", "c1", qm(t, `{}`), &fakeMongoExecutor{})
	assert.EqualError(t, err, "find 操作需要 database 和 collection 参数")
	_, err = runOp(t, "find", "db1", "", qm(t, `{}`), &fakeMongoExecutor{})
	assert.EqualError(t, err, "find 操作需要 database 和 collection 参数")
}

func TestMongoFindOneShared(t *testing.T) {
	fake := &fakeMongoExecutor{doc: json.RawMessage(`{"_id":"1"}`), found: true}
	out, err := runOp(t, "findOne", "db1", "c1", qm(t, `{"filter":{"_id":"1"}}`), fake)
	require.NoError(t, err)
	assert.JSONEq(t, `{"document":{"_id":"1"}}`, out)
	assert.JSONEq(t, `{"_id":"1"}`, string(fake.filter))

	// 无匹配返回 document:null。
	fake2 := &fakeMongoExecutor{found: false}
	out, err = runOp(t, "findOne", "db1", "c1", qm(t, `{}`), fake2)
	require.NoError(t, err)
	assert.JSONEq(t, `{"document":null}`, out)
}

func TestMongoInsertOneShared(t *testing.T) {
	fake := &fakeMongoExecutor{insertedID: "507f1f77"}
	out, err := runOp(t, "insertOne", "db1", "c1", qm(t, `{"document":{"name":"x"}}`), fake)
	require.NoError(t, err)
	assert.JSONEq(t, `{"insertedId":"507f1f77"}`, out)
	assert.JSONEq(t, `{"name":"x"}`, string(fake.document))

	_, err = runOp(t, "insertOne", "db1", "c1", qm(t, `{}`), &fakeMongoExecutor{})
	assert.EqualError(t, err, "insertOne 操作需要 document 参数")
}

func TestMongoInsertManyShared(t *testing.T) {
	fake := &fakeMongoExecutor{ids: []string{"a", "b"}}
	out, err := runOp(t, "insertMany", "db1", "c1", qm(t, `{"documents":[{"n":1},{"n":2}]}`), fake)
	require.NoError(t, err)
	assert.JSONEq(t, `{"insertedIds":["a","b"],"count":2}`, out)
	assert.Len(t, fake.documents, 2)

	_, err = runOp(t, "insertMany", "db1", "c1", qm(t, `{"documents":"oops"}`), &fakeMongoExecutor{})
	assert.ErrorContains(t, err, "解析 documents 数组失败")
}

func TestMongoUpdateShared(t *testing.T) {
	for _, op := range []string{"updateOne", "updateMany"} {
		fake := &fakeMongoExecutor{matched: 1, modified: 1}
		out, err := runOp(t, op, "db1", "c1", qm(t, `{"filter":{"x":1},"update":{"$set":{"x":2}}}`), fake)
		require.NoError(t, err)
		assert.JSONEq(t, `{"matchedCount":1,"modifiedCount":1}`, out)
		assert.JSONEq(t, `{"x":1}`, string(fake.filter))
		assert.JSONEq(t, `{"$set":{"x":2}}`, string(fake.update))

		// update 缺失时报操作名。
		_, err = runOp(t, op, "db1", "c1", qm(t, `{"filter":{"x":1}}`), &fakeMongoExecutor{})
		assert.EqualError(t, err, op+" 操作需要 update 参数")

		// update 为字面量 null 与缺失等价。
		_, err = runOp(t, op, "db1", "c1", qm(t, `{"filter":{"x":1},"update":null}`), &fakeMongoExecutor{})
		assert.EqualError(t, err, op+" 操作需要 update 参数")
	}
}

func TestMongoDeleteShared(t *testing.T) {
	for _, op := range []string{"deleteOne", "deleteMany"} {
		fake := &fakeMongoExecutor{deleted: 2}
		out, err := runOp(t, op, "db1", "c1", qm(t, `{"filter":{"x":1}}`), fake)
		require.NoError(t, err)
		assert.JSONEq(t, `{"deletedCount":2}`, out)
		assert.JSONEq(t, `{"x":1}`, string(fake.filter))
	}
}

func TestMongoAggregateShared(t *testing.T) {
	fake := &fakeMongoExecutor{docs: []json.RawMessage{json.RawMessage(`{"total":3}`)}}
	out, err := runOp(t, "aggregate", "db1", "c1", qm(t, `{"pipeline":[{"$group":{"_id":null,"total":{"$sum":1}}}]}`), fake)
	require.NoError(t, err)
	assert.JSONEq(t, `{"documents":[{"total":3}],"count":1}`, out)
	assert.JSONEq(t, `[{"$group":{"_id":null,"total":{"$sum":1}}}]`, string(fake.pipeline))

	_, err = runOp(t, "aggregate", "db1", "c1", qm(t, `{}`), &fakeMongoExecutor{})
	assert.EqualError(t, err, "aggregate 操作需要 pipeline 参数")
	_, err = runOp(t, "aggregate", "db1", "c1", qm(t, `{"pipeline":"oops"}`), &fakeMongoExecutor{})
	assert.ErrorContains(t, err, "解析 pipeline 数组失败")
}

func TestMongoCountDocumentsShared(t *testing.T) {
	fake := &fakeMongoExecutor{count: 5}
	out, err := runOp(t, "countDocuments", "db1", "c1", qm(t, `{"filter":{"x":1}}`), fake)
	require.NoError(t, err)
	assert.JSONEq(t, `{"count":5}`, out)
	assert.JSONEq(t, `{"x":1}`, string(fake.filter))
}

func TestMongoListDatabasesShared(t *testing.T) {
	out, err := runOp(t, "listDatabases", "", "", qm(t, `{}`), &fakeMongoExecutor{})
	require.NoError(t, err)
	assert.JSONEq(t, `{"databases":["admin","test"],"count":2}`, out)
}

func TestMongoListCollectionsShared(t *testing.T) {
	out, err := runOp(t, "listCollections", "db1", "", qm(t, `{}`), &fakeMongoExecutor{})
	require.NoError(t, err)
	assert.JSONEq(t, `{"collections":["items"],"count":1}`, out)

	_, err = runOp(t, "listCollections", "", "", qm(t, `{}`), &fakeMongoExecutor{})
	assert.EqualError(t, err, "database 参数不能为空")
}

// 所有 12 个操作都必须能走通 mongoOps 分发；漏掉任意一个（比如删表项时忘了
// 别的调用方）会在这里立即失败，而不是在 legacy/默认两种资产上悄悄分叉。
func TestMongoOpsEveryOpHasExecAndRuns(t *testing.T) {
	for op := range mongoOps {
		if op == "listDatabases" {
			continue // 其余 11 个的默认参数形态不通用（listDatabases 无 db/coll）
		}
		fake := &fakeMongoExecutor{}
		_, err := runOp(t, op, "db1", "c1", qm(t, `{}`), fake)
		// 这里只关心分发不 panic、exec 非 nil；参数校验错误是各 op 自己的测试。
		if err != nil {
			// 共享校验（缺参）会返回错误——能走到这一步说明 exec 被正确调用了。
			require.ErrorContains(t, err, "需要")
		} else {
			require.NotEmpty(t, fake.db)
		}
	}
}

// executorFor 按连接模式选适配器：legacy -> v1，默认 -> v2。二者都必须实现
// mongoDBExecutor（编译期保证），这里钉住"分流不串"。
func TestExecutorForDispatch(t *testing.T) {
	_, isV1 := executorFor(&connpool.MongoClientCloser{Legacy: true}).(*mongoExecutorV1)
	assert.True(t, isV1)
	_, isV2 := executorFor(&connpool.MongoClientCloser{Legacy: false}).(*mongoExecutorV2)
	assert.True(t, isV2)
}

// ExecuteMongoDB 在不依赖真实连接的路径上（未知操作/坏查询）先于驱动报错。
func TestExecuteMongoDBValidationBeforeDriver(t *testing.T) {
	ctx := context.Background()
	_, err := ExecuteMongoDB(ctx, &connpool.MongoClientCloser{Legacy: false}, "", "", "nope", "{}")
	assert.ErrorContains(t, err, "不支持的 MongoDB 操作")
	_, err = ExecuteMongoDB(ctx, &connpool.MongoClientCloser{Legacy: true}, "", "", "find", "not-json")
	assert.ErrorContains(t, err, "无效的查询参数")
}
