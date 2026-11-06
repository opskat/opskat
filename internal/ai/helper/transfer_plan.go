package helper

import (
	"fmt"
	"os"
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
}

// PlanTransfer 判定传输形态。localDestination 表示目的端是本机路径：多源时目的端必须写成
// 目录，远端（POSIX 路径）以 "/" 结尾，本机路径还可以用本机的路径分隔符结尾（Windows 的 "\"）。
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
	return plan, nil
}
