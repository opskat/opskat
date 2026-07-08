package oss

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDeriveUploadKeyJoinsPrefixAndBase(t *testing.T) {
	assert.Equal(t, "images/hero.jpg", deriveUploadKey("images/", "/Users/me/pics/hero.jpg"))
	assert.Equal(t, "hero.jpg", deriveUploadKey("", "/Users/me/pics/hero.jpg"))
}
