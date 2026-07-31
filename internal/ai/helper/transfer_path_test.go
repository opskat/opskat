package helper

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/opskat/opskat/internal/model/entity/asset_entity"
)

// stubResolver 替代注入的 ResolveAssetFunc。解析策略属于各自的入口（opsctl 的 resolveAsset
// 认组路径，AI 侧的 assetref.Resolve 认 id / 精确名），解析器这一层只关心"解析出来 / 解析
// 不出来"两种回答，所以这里不复刻任何一家的查询实现，只记录被问到的 ref。
type stubResolver struct {
	assets map[string]*asset_entity.Asset
	refs   []string
}

func (s *stubResolver) resolve(_ context.Context, ref string) (*asset_entity.Asset, error) {
	s.refs = append(s.refs, ref)
	if a, ok := s.assets[ref]; ok {
		return a, nil
	}
	return nil, errors.New("no asset found matching " + ref)
}

func newStubResolver(assets ...*asset_entity.Asset) *stubResolver {
	byName := make(map[string]*asset_entity.Asset, len(assets))
	for _, a := range assets {
		byName[a.Name] = a
	}
	return &stubResolver{assets: byName}
}

func TestParseTransferEndpoint_Remote(t *testing.T) {
	assets := []*asset_entity.Asset{
		{ID: 8, Name: "web-01", Type: asset_entity.AssetTypeSSH},
		{ID: 3, Name: "s3-prod", Type: asset_entity.AssetTypeSSH},
		// 前缀里带 "/"：解析策略是入口自己的事，解析器必须把第一个冒号前的整段原样交给
		// 注入的解析器，不做任何切分——opsctl 正是靠这一点保住 "组路径/名称" 的写法。
		{ID: 9, Name: "production/web-01", Type: asset_entity.AssetTypeSSH},
		{ID: 7, Name: "7", Type: asset_entity.AssetTypeSSH},
	}
	cases := []struct {
		in       string
		wantRef  string
		wantID   int64
		wantPath string
	}{
		{"7:/etc/hosts", "7", 7, "/etc/hosts"},
		{"web-01:/var/log/app.log", "web-01", 8, "/var/log/app.log"},
		{"production/web-01:/etc/hosts", "production/web-01", 9, "/etc/hosts"},
		{"s3-prod:/bucket/key.txt", "s3-prod", 3, "/bucket/key.txt"},
		// 路径里的冒号属于路径：只按第一个冒号切。
		{"web-01:/tmp/a:b.log", "web-01", 8, "/tmp/a:b.log"},
	}
	for _, c := range cases {
		r := newStubResolver(assets...)
		asset, path, err := ParseTransferEndpoint(context.Background(), c.in, r.resolve)
		if err != nil {
			t.Fatalf("ParseTransferEndpoint(%q) unexpected error: %v", c.in, err)
		}
		if asset == nil || asset.ID != c.wantID {
			t.Fatalf("ParseTransferEndpoint(%q) asset = %+v, want id %d", c.in, asset, c.wantID)
		}
		if path != c.wantPath {
			t.Fatalf("ParseTransferEndpoint(%q) path = %q, want %q", c.in, path, c.wantPath)
		}
		if len(r.refs) != 1 || r.refs[0] != c.wantRef {
			t.Fatalf("ParseTransferEndpoint(%q) asked resolver for %q, want [%q]", c.in, r.refs, c.wantRef)
		}
	}
}

// 无冒号 / 冒号前为空 / Windows 盘符 / 前缀不是资产，四种形态都必须留在本地且路径原样返回。
// 前两种连解析器都不该问——一个杂散的冒号不该触发资产查询，更不该触发 D15 守卫。
func TestParseTransferEndpoint_Local(t *testing.T) {
	cases := []struct {
		in       string
		wantRefs []string
	}{
		{"./local-file.txt", nil},
		{"/absolute/path", nil},
		{":foo", nil},
		// D15 守卫会查一次前缀（"C" 不是资产），查不到就仍是本地路径。
		{`C:\windows`, []string{"C"}},
		{"note:todo", []string{"note"}},
	}
	for _, c := range cases {
		r := newStubResolver(&asset_entity.Asset{ID: 8, Name: "web-01", Type: asset_entity.AssetTypeSSH})
		asset, path, err := ParseTransferEndpoint(context.Background(), c.in, r.resolve)
		if err != nil {
			t.Fatalf("ParseTransferEndpoint(%q) unexpected error: %v", c.in, err)
		}
		if asset != nil {
			t.Fatalf("ParseTransferEndpoint(%q) asset = %+v, want nil (local path)", c.in, asset)
		}
		if path != c.in {
			t.Fatalf("ParseTransferEndpoint(%q) path = %q, want the input unchanged", c.in, path)
		}
		if strings.Join(r.refs, ",") != strings.Join(c.wantRefs, ",") {
			t.Fatalf("ParseTransferEndpoint(%q) asked resolver for %q, want %q", c.in, r.refs, c.wantRefs)
		}
	}
}

// D15 守卫本体：前缀解析成了真资产，但冒号后没有前导 "/"。必须报错并明示 "<asset>:/<path>"
// 的写法，而不是把整串当本地路径吞掉。
func TestParseTransferEndpoint_MissingLeadingSlashErrorsWhenPrefixIsAsset(t *testing.T) {
	r := newStubResolver(&asset_entity.Asset{ID: 8, Name: "web-01", Type: asset_entity.AssetTypeSSH})

	asset, path, err := ParseTransferEndpoint(context.Background(), "web-01:etc/hosts", r.resolve)

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "web-01:/<path>") {
		t.Fatalf("error %q does not name the correct <asset>:/<path> form", err)
	}
	if asset != nil || path != "" {
		t.Fatalf(`got (%+v, %q), want (nil, "") on error`, asset, path)
	}
}

// 前导 "/" 一旦出现就锁定了"这是远端路径"的解读：前缀解析失败必须原样上报，不能悄悄回落成
// 本地路径，否则一个拼错的资产名会变成对着字面串 "unknown:/etc/passwd" 的"文件不存在"。
func TestParseTransferEndpoint_UnresolvablePrefixWithSlashErrors(t *testing.T) {
	r := newStubResolver()

	asset, path, err := ParseTransferEndpoint(context.Background(), "unknown:/etc/passwd", r.resolve)

	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), `resolving asset "unknown"`) {
		t.Fatalf("error %q does not name the failing reference", err)
	}
	if asset != nil || path != "" {
		t.Fatalf(`got (%+v, %q), want (nil, "") on error`, asset, path)
	}
}
