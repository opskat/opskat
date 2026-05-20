package audit

import (
	"testing"

	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
)

func TestTruncateString(t *testing.T) {
	convey.Convey("字符串截断", t, func() {
		convey.Convey("短字符串不截断", func() {
			assert.Equal(t, "hello", truncateString("hello", 10))
		})

		convey.Convey("超长字符串截断到指定长度并追加标记", func() {
			result := truncateString("abcdefghij", 5)
			assert.Equal(t, "abcde\n...[truncated]", result)
		})

		convey.Convey("空字符串返回空", func() {
			assert.Equal(t, "", truncateString("", 10))
		})
	})
}
