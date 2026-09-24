package bootstrap

import (
	"context"

	"github.com/opskat/opskat/internal/model/entity/extension_describe_entity"
	"github.com/opskat/opskat/internal/repository/extension_describe_repo"
)

// extensionDescribeCache adapts extension_describe_repo to the DescribeCache seam
// in pkg/extension: the repository owns storage, pkg/extension owns the question
// ("what did this exact wasm binary say it can do?").
//
// It is wired here rather than in a service because every process that reads
// extensions goes through bootstrap — the desktop app and opsctl alike — and opsctl
// has no extension service at all, only the manifest reader.
//
// The seam is context-free on purpose: it is also consulted from
// extension.LoadManifestInfo, a plain directory read with no request to attach to.
type extensionDescribeCache struct{}

func (extensionDescribeCache) LoadDescriptor(name string) (string, []byte, error) {
	row, err := extension_describe_repo.ExtensionDescribe().Find(context.Background(), name)
	if err != nil || row == nil {
		return "", nil, err
	}
	return row.WasmHash, []byte(row.Descriptor), nil
}

func (extensionDescribeCache) StoreDescriptor(name, wasmHash string, payload []byte) error {
	return extension_describe_repo.ExtensionDescribe().Save(context.Background(),
		&extension_describe_entity.ExtensionDescribe{
			Name:       name,
			WasmHash:   wasmHash,
			Descriptor: string(payload),
		})
}

func (extensionDescribeCache) DeleteDescriptor(name string) error {
	return extension_describe_repo.ExtensionDescribe().Delete(context.Background(), name)
}
