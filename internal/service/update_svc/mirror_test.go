package update_svc

import (
	"testing"

	"github.com/smartystreets/goconvey/convey"
	"github.com/stretchr/testify/assert"
)

func TestApplyMirror(t *testing.T) {
	convey.Convey("镜像 URL 改写", t, func() {
		convey.Convey("空镜像返回原始 URL", func() {
			url := applyMirror("https://github.com/opskat/opskat/releases/download/v1.0.0/file.tar.gz", "")
			assert.Equal(t, "https://github.com/opskat/opskat/releases/download/v1.0.0/file.tar.gz", url)
		})

		convey.Convey("带尾部斜杠的镜像前缀", func() {
			url := applyMirror("https://github.com/opskat/opskat/releases/download/v1.0.0/file.tar.gz", "https://ghfast.top/")
			assert.Equal(t, "https://ghfast.top/https://github.com/opskat/opskat/releases/download/v1.0.0/file.tar.gz", url)
		})

		convey.Convey("不带尾部斜杠的镜像前缀自动补齐", func() {
			url := applyMirror("https://github.com/opskat/opskat/releases/download/v1.0.0/file.tar.gz", "https://ghfast.top")
			assert.Equal(t, "https://ghfast.top/https://github.com/opskat/opskat/releases/download/v1.0.0/file.tar.gz", url)
		})

		convey.Convey("可改写 api.github.com URL", func() {
			url := applyMirror("https://api.github.com/repos/opskat/opskat/releases/latest", "https://ghfast.top/")
			assert.Equal(t, "https://ghfast.top/https://api.github.com/repos/opskat/opskat/releases/latest", url)
		})

		convey.Convey("可改写 raw.githubusercontent.com URL", func() {
			url := applyMirror("https://raw.githubusercontent.com/opskat/opskat/main/file.json", "https://ghfast.top/")
			assert.Equal(t, "https://ghfast.top/https://raw.githubusercontent.com/opskat/opskat/main/file.json", url)
		})
	})
}

func TestGetAvailableMirrors(t *testing.T) {
	convey.Convey("可用镜像列表", t, func() {
		mirrors := GetAvailableMirrors()

		convey.Convey("第一个是 GitHub 默认（无前缀）", func() {
			assert.Equal(t, "github", mirrors[0].ID)
			assert.Equal(t, "", mirrors[0].URL)
		})

		convey.Convey("包含 ghfast.top", func() {
			found := false
			for _, m := range mirrors {
				if m.ID == "ghfast" {
					assert.Equal(t, "https://ghfast.top/", m.URL)
					found = true
				}
			}
			assert.True(t, found)
		})

		convey.Convey("包含 gh-proxy.com", func() {
			found := false
			for _, m := range mirrors {
				if m.ID == "gh-proxy" {
					assert.Equal(t, "https://gh-proxy.com/", m.URL)
					found = true
				}
			}
			assert.True(t, found)
		})

		convey.Convey("至少有 3 个选项", func() {
			assert.GreaterOrEqual(t, len(mirrors), 3)
		})
	})
}
