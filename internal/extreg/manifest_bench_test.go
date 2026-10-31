package extreg

import (
	"fmt"
	"strings"
	"testing"

	"github.com/opskat/opskat/pkg/extension"
)

func describedBench(b *testing.B, describeJSON string) *extension.Manifest {
	b.Helper()
	m, err := extension.ParseManifest([]byte(wireManifestJSON))
	if err != nil {
		b.Fatal(err)
	}
	d, err := extension.ParseDescriptor([]byte(describeJSON))
	if err != nil {
		b.Fatal(err)
	}
	return extension.Described(m.Manifest, d)
}

// BenchmarkManifestOf is the measurement record behind "ManifestOf decodes on
// every call, with no cache": the cost of one call on the wire fixture (one asset
// type, two tools, pages, snippets, a driver — Elasticsearch-sized, which has
// seven tools) and on a manifest with 40 tools, several times the largest shipped
// extension.
func BenchmarkManifestOf(b *testing.B) {
	var tools []string
	for i := 0; i < 40; i++ {
		tools = append(tools, fmt.Sprintf(`{"name":"tool%d","i18n":{"description":"d"},"policyAction":"a%d",`+
			`"parameters":{"type":"object","properties":{"body":{"type":"string"},"index":{"type":"string"},"size":{"type":"integer"}}},"timeoutMs":3000}`, i, i%8))
	}
	for _, c := range []struct {
		name     string
		describe string
	}{
		{"wire", wireDescribeJSON},
		{"tools40", strings.Replace(wireDescribeJSON, `"tools":[`, `"tools":[`+strings.Join(tools, ",")+",", 1)},
	} {
		m := describedBench(b, c.describe)
		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				if _, err := ManifestOf(m); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
