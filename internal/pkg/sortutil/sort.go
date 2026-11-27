package sortutil

import "fmt"

// ReorderSiblings 把 movedID 从 siblings 中抽出，插到 beforeID 之前；
// beforeID == 0 或未在 siblings 中找到时，追加到末尾。
// 返回重排后的切片；若 movedID 未在 siblings 中找到，原样返回。
func ReorderSiblings[T any](siblings []T, movedID, beforeID int64, getID func(T) int64) []T {
	return ReorderSiblingsBatch(siblings, []int64{movedID}, beforeID, getID)
}

// ReorderSiblingsBatch 把 movedIDs（去重并保持调用方顺序）从 siblings 中抽出，整体插到 beforeID 之前；
// beforeID == 0 或未在剩余元素中找到时，追加到末尾。若 beforeID 落在 movedIDs 内，则插到其后首个未移动元素之前。
// 返回重排后的切片；若 movedIDs 中没有任何元素在 siblings 中，原样返回。
func ReorderSiblingsBatch[T any](siblings []T, movedIDs []int64, beforeID int64, getID func(T) int64) []T {
	if len(movedIDs) == 0 || len(siblings) == 0 {
		return siblings
	}

	byID := make(map[int64]T, len(siblings))
	for _, item := range siblings {
		byID[getID(item)] = item
	}

	movedSet := make(map[int64]struct{}, len(movedIDs))
	moved := make([]T, 0, len(movedIDs))
	for _, id := range movedIDs {
		if _, dup := movedSet[id]; dup {
			continue
		}
		item, ok := byID[id]
		if !ok {
			continue
		}
		movedSet[id] = struct{}{}
		moved = append(moved, item)
	}
	if len(moved) == 0 {
		return siblings
	}

	effectiveBeforeID := beforeID
	if effectiveBeforeID != 0 {
		if _, inMoved := movedSet[effectiveBeforeID]; inMoved {
			effectiveBeforeID = 0
			foundAnchor := false
			for _, item := range siblings {
				id := getID(item)
				if !foundAnchor {
					if id == beforeID {
						foundAnchor = true
					}
					continue
				}
				if _, m := movedSet[id]; !m {
					effectiveBeforeID = id
					break
				}
			}
		}
	}

	rest := make([]T, 0, len(siblings)-len(moved))
	for _, item := range siblings {
		if _, m := movedSet[getID(item)]; m {
			continue
		}
		rest = append(rest, item)
	}

	if effectiveBeforeID == 0 {
		return append(rest, moved...)
	}

	result := make([]T, 0, len(siblings))
	inserted := false
	for _, item := range rest {
		if !inserted && getID(item) == effectiveBeforeID {
			result = append(result, moved...)
			inserted = true
		}
		result = append(result, item)
	}
	if !inserted {
		result = append(result, moved...)
	}
	return result
}

// MoveItem 通用排序移动逻辑（up/down/top）
func MoveItem[T any](id int64, direction string, items []T,
	getID func(T) int64, getOrder func(T) int, updateOrder func(int64, int) error,
) error {
	idx := -1
	for i, item := range items {
		if getID(item) == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("item not found")
	}

	if hasDuplicateOrder(items, getOrder) {
		reordered, changed, err := moveItemInSlice(direction, items, idx)
		if err != nil {
			return err
		}
		if !changed {
			return nil
		}
		for i, item := range reordered {
			if err := updateOrder(getID(item), (i+1)*10); err != nil {
				return err
			}
		}
		return nil
	}

	switch direction {
	case "up":
		if idx == 0 {
			return nil
		}
		prevOrder := getOrder(items[idx-1])
		curOrder := getOrder(items[idx])
		if prevOrder == curOrder {
			curOrder = prevOrder + 1
		}
		if err := updateOrder(getID(items[idx]), prevOrder); err != nil {
			return err
		}
		return updateOrder(getID(items[idx-1]), curOrder)
	case "down":
		if idx == len(items)-1 {
			return nil
		}
		nextOrder := getOrder(items[idx+1])
		curOrder := getOrder(items[idx])
		if nextOrder == curOrder {
			nextOrder = curOrder + 1
		}
		if err := updateOrder(getID(items[idx]), nextOrder); err != nil {
			return err
		}
		return updateOrder(getID(items[idx+1]), curOrder)
	case "top":
		if idx == 0 {
			return nil
		}
		firstOrder := getOrder(items[0])
		return updateOrder(id, firstOrder-1)
	default:
		return fmt.Errorf("invalid direction: %s", direction)
	}
}

func hasDuplicateOrder[T any](items []T, getOrder func(T) int) bool {
	seen := make(map[int]struct{}, len(items))
	for _, item := range items {
		order := getOrder(item)
		if _, ok := seen[order]; ok {
			return true
		}
		seen[order] = struct{}{}
	}
	return false
}

func moveItemInSlice[T any](direction string, items []T, idx int) ([]T, bool, error) {
	next := append([]T(nil), items...)
	switch direction {
	case "up":
		if idx == 0 {
			return next, false, nil
		}
		next[idx-1], next[idx] = next[idx], next[idx-1]
		return next, true, nil
	case "down":
		if idx == len(next)-1 {
			return next, false, nil
		}
		next[idx], next[idx+1] = next[idx+1], next[idx]
		return next, true, nil
	case "top":
		if idx == 0 {
			return next, false, nil
		}
		moved := next[idx]
		copy(next[1:idx+1], next[0:idx])
		next[0] = moved
		return next, true, nil
	default:
		return nil, false, fmt.Errorf("invalid direction: %s", direction)
	}
}
