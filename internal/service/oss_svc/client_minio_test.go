package oss_svc

import (
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/stretchr/testify/assert"
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
