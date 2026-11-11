package helper

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// PlannedTransferSource is the protocol-neutral shape of one cp source.
type PlannedTransferSource struct {
	Path    string
	Expands bool
	ReadDir Direction
}

// TransferPlan owns the shape rules shared by the AI and opsctl entrances.
type TransferPlan struct {
	Sources  []PlannedTransferSource
	Multiple bool
	// Destination 是单源形态（Multiple 为假）的具体落点：目的端写成目录形态时是
	// <目录><源文件名>，否则就是目的端字面量。多源形态下它就是目的基点，各条目的落点由
	// 调用方按 RelPath 拼。审批与写入都用它，两者因此是同一条路径。
	Destination string
}

// PlanTransfer 判定传输形态。localDestination 表示目的端是本机路径：目的端的目录形态是
// 远端（POSIX 路径）以 "/" 结尾，本机路径还可以用本机的路径分隔符结尾（Windows 的 "\"）。
// 多源时目的端必须写成目录；单源写成目录时落到该目录下并保留源文件名（#364）——这是语法
// 判定，不探测目的端是否存在，落点在审批之前就已确定（D16）。
func PlanTransfer(sourcePaths []string, destination string, localDestination, recursive bool) (*TransferPlan, error) {
	plan := &TransferPlan{Sources: make([]PlannedTransferSource, 0, len(sourcePaths))}
	for _, source := range sourcePaths {
		expands := recursive || HasGlobPattern(source)
		dir := DirRead
		if expands {
			dir = DirReadScope
		}
		plan.Sources = append(plan.Sources, PlannedTransferSource{Path: source, Expands: expands, ReadDir: dir})
		plan.Multiple = plan.Multiple || expands
	}
	plan.Multiple = plan.Multiple || len(sourcePaths) > 1
	isDir := strings.HasSuffix(destination, "/") ||
		(localDestination && strings.HasSuffix(destination, string(os.PathSeparator)))
	if plan.Multiple && !isDir {
		return nil, fmt.Errorf("destination %q must end with \"/\" when there is more than one source (multiple arguments, --recursive, or a glob pattern): each entry lands at <destination>/<path relative to the source base>", destination)
	}
	plan.Destination = destination
	if !plan.Multiple && isDir {
		// filepath.Base 而非 path.Base：本地源在 Windows 上是 `\` 分隔的，远端路径恒为 `/`
		// 分隔，filepath 在 Windows 上两种都认——与 opsctl 指名源的 RelPath 同一取法。
		plan.Destination = destination + filepath.Base(sourcePaths[0])
	}
	return plan, nil
}
