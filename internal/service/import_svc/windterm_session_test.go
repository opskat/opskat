package import_svc

import (
	"testing"

	. "github.com/smartystreets/goconvey/convey"
)

func TestWindTermImportSession(t *testing.T) {
	Convey("WindTerm 导入会话单槽缓存", t, func() {
		Convey("写入后可按 id 取回相同内容", func() {
			id, err := NewWindTermImportSession([]byte("hello"))
			So(err, ShouldBeNil)
			data, ok := WindTermImportSessionData(id)
			So(ok, ShouldBeTrue)
			So(string(data), ShouldEqual, "hello")
		})

		Convey("新预览顶掉旧的，旧 id 失效", func() {
			oldID, _ := NewWindTermImportSession([]byte("old"))
			newID, _ := NewWindTermImportSession([]byte("new"))

			_, ok := WindTermImportSessionData(oldID)
			So(ok, ShouldBeFalse)

			data, ok := WindTermImportSessionData(newID)
			So(ok, ShouldBeTrue)
			So(string(data), ShouldEqual, "new")
		})

		Convey("删除后内容被清空", func() {
			id, _ := NewWindTermImportSession([]byte("x"))
			DeleteWindTermImportSession(id)
			_, ok := WindTermImportSessionData(id)
			So(ok, ShouldBeFalse)
		})

		Convey("空 id 不命中", func() {
			_, _ = NewWindTermImportSession([]byte("x"))
			_, ok := WindTermImportSessionData("")
			So(ok, ShouldBeFalse)
		})

		Convey("删除非当前 id 不影响当前缓存", func() {
			id, _ := NewWindTermImportSession([]byte("keep"))
			DeleteWindTermImportSession("some-stale-id")
			data, ok := WindTermImportSessionData(id)
			So(ok, ShouldBeTrue)
			So(string(data), ShouldEqual, "keep")
		})
	})
}
