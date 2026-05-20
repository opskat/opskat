package redis

import (
	"github.com/opskat/opskat/internal/app/i18n"
	"github.com/opskat/opskat/internal/service/redis_svc"
)

func (r *Redis) RedisListDatabases(assetID int64) ([]redis_svc.RedisDatabase, error) {
	return r.service.ListDatabases(i18n.Ctx(r.ctx, r.lang.Lang()), assetID)
}

func (r *Redis) RedisScanKeys(req redis_svc.RedisScanRequest) (redis_svc.RedisScanResponse, error) {
	return r.service.ScanKeys(i18n.Ctx(r.ctx, r.lang.Lang()), req)
}

func (r *Redis) RedisGetKeyDetail(req redis_svc.RedisKeyRequest) (redis_svc.RedisKeyDetail, error) {
	return r.service.GetKeyDetail(i18n.Ctx(r.ctx, r.lang.Lang()), req)
}

func (r *Redis) RedisSetKeyTTL(assetID int64, db int, key string, seconds int64) error {
	return r.service.SetKeyTTL(i18n.Ctx(r.ctx, r.lang.Lang()), assetID, db, key, seconds)
}

func (r *Redis) RedisPersistKey(assetID int64, db int, key string) error {
	return r.service.PersistKey(i18n.Ctx(r.ctx, r.lang.Lang()), assetID, db, key)
}

func (r *Redis) RedisRenameKey(assetID int64, db int, oldKey string, newKey string) error {
	return r.service.RenameKey(i18n.Ctx(r.ctx, r.lang.Lang()), assetID, db, oldKey, newKey)
}

func (r *Redis) RedisDeleteKeys(assetID int64, db int, keys []string) error {
	return r.service.DeleteKeys(i18n.Ctx(r.ctx, r.lang.Lang()), assetID, db, keys)
}

func (r *Redis) RedisSetStringValue(req redis_svc.RedisStringSetRequest) error {
	return r.service.SetStringValue(i18n.Ctx(r.ctx, r.lang.Lang()), req)
}

func (r *Redis) RedisHashSet(assetID int64, db int, key string, field string, value string) error {
	return r.service.HashSet(i18n.Ctx(r.ctx, r.lang.Lang()), assetID, db, key, field, value)
}

func (r *Redis) RedisHashDelete(assetID int64, db int, key string, field string) error {
	return r.service.HashDelete(i18n.Ctx(r.ctx, r.lang.Lang()), assetID, db, key, field)
}

func (r *Redis) RedisListPush(assetID int64, db int, key string, value string) error {
	return r.service.ListPush(i18n.Ctx(r.ctx, r.lang.Lang()), assetID, db, key, value)
}

func (r *Redis) RedisListSet(assetID int64, db int, key string, index int64, value string) error {
	return r.service.ListSet(i18n.Ctx(r.ctx, r.lang.Lang()), assetID, db, key, index, value)
}

func (r *Redis) RedisListDelete(assetID int64, db int, key string, index int64) error {
	return r.service.ListDelete(i18n.Ctx(r.ctx, r.lang.Lang()), assetID, db, key, index)
}

func (r *Redis) RedisSetAdd(assetID int64, db int, key string, member string) error {
	return r.service.SetAdd(i18n.Ctx(r.ctx, r.lang.Lang()), assetID, db, key, member)
}

func (r *Redis) RedisSetRemove(assetID int64, db int, key string, member string) error {
	return r.service.SetRemove(i18n.Ctx(r.ctx, r.lang.Lang()), assetID, db, key, member)
}

func (r *Redis) RedisZSetAdd(assetID int64, db int, key string, member string, score float64) error {
	return r.service.ZSetAdd(i18n.Ctx(r.ctx, r.lang.Lang()), assetID, db, key, member, score)
}

func (r *Redis) RedisZSetRemove(assetID int64, db int, key string, member string) error {
	return r.service.ZSetRemove(i18n.Ctx(r.ctx, r.lang.Lang()), assetID, db, key, member)
}

func (r *Redis) RedisStreamAdd(assetID int64, db int, key string, id string, fields []redis_svc.RedisStreamField) error {
	return r.service.StreamAdd(i18n.Ctx(r.ctx, r.lang.Lang()), assetID, db, key, id, fields)
}

func (r *Redis) RedisStreamDelete(assetID int64, db int, key string, ids []string) error {
	return r.service.StreamDelete(i18n.Ctx(r.ctx, r.lang.Lang()), assetID, db, key, ids)
}

func (r *Redis) RedisClientList(assetID int64) (string, error) {
	return r.service.ClientList(i18n.Ctx(r.ctx, r.lang.Lang()), assetID)
}

func (r *Redis) RedisSlowLog(assetID int64, limit int64) ([]redis_svc.RedisSlowLogEntry, error) {
	return r.service.SlowLog(i18n.Ctx(r.ctx, r.lang.Lang()), assetID, limit)
}

func (r *Redis) RedisCommandHistory(assetID int64, limit int) []redis_svc.CommandHistoryEntry {
	return r.service.CommandHistory(assetID, limit)
}

func (r *Redis) RedisFormatValue(value string, format string) redis_svc.RedisFormattedValue {
	return redis_svc.FormatDisplayValue(value, format)
}

func (r *Redis) RedisEncodeValue(value string, format string) (string, error) {
	return redis_svc.EncodeValueForStorage(value, format)
}
