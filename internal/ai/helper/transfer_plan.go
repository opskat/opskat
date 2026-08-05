package helper

import (
	"fmt"
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

func PlanTransfer(sourcePaths []string, destination string, recursive bool) (*TransferPlan, error) {
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
	if plan.Multiple && !strings.HasSuffix(destination, "/") {
		return nil, fmt.Errorf("destination %q must end with \"/\" when there is more than one source (multiple arguments, --recursive, or a glob pattern): each entry lands at <destination>/<path relative to the source base>", destination)
	}
	return plan, nil
}
