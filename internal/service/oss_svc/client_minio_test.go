package oss_svc

import (
	"errors"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestToObjectItemDetectsFolderPrefix(t *testing.T) {
	got := toObjectItem(minio.ObjectInfo{Key: "images/thumbnails/", Size: 0})
	assert.True(t, got.IsPrefix)
}

func TestToObjectItemMapsObject(t *testing.T) {
	got := toObjectItem(minio.ObjectInfo{
		Key: "images/hero.jpg", Size: 2516480, ETag: "9b2c", LastModified: time.Unix(1751811127, 0),
	})
	assert.False(t, got.IsPrefix)
	assert.Equal(t, int64(2516480), got.Size)
	assert.Equal(t, int64(1751811127), got.LastModified)
	assert.Equal(t, "9b2c", got.ETag)
}

func TestPresignExpiryDefaultsToOneHourWhenNonPositive(t *testing.T) {
	assert.Equal(t, time.Hour, presignExpiry(0))
	assert.Equal(t, time.Hour, presignExpiry(-5))
}

func TestPresignExpiryUsesGivenSeconds(t *testing.T) {
	assert.Equal(t, 120*time.Second, presignExpiry(120))
}

func TestAggregateRemoveErrorsNilWhenEmpty(t *testing.T) {
	assert.NoError(t, aggregateRemoveErrors(nil))
	assert.NoError(t, aggregateRemoveErrors([]minio.RemoveObjectError{}))
}

func TestAggregateRemoveErrorsReportsEachFailure(t *testing.T) {
	err := aggregateRemoveErrors([]minio.RemoveObjectError{
		{ObjectName: "logs/a.txt", Err: errors.New("AccessDenied")},
		{ObjectName: "logs/b.txt", Err: errors.New("NoSuchKey")},
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "logs/a.txt")
	assert.Contains(t, err.Error(), "AccessDenied")
	assert.Contains(t, err.Error(), "logs/b.txt")
}
