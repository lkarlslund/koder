package store

import (
	"slices"
	"strings"
)

// pageOrder is the total order a paged list and its cursor share: records
// sort by sortValue, ties break by objectID, and descending reverses both.
type pageOrder[T any] struct {
	sortValue  func(T) string
	objectID   func(T) string
	descending bool
}

func (o pageOrder[T]) compare(sortValue, objectID string, otherSortValue, otherObjectID string) int {
	order := strings.Compare(sortValue, otherSortValue)
	if order == 0 {
		order = strings.Compare(objectID, otherObjectID)
	}
	if o.descending {
		return -order
	}
	return order
}

// paginate filters records, sorts them in order, and returns up to limit
// records after cursor, plus the cursor for the following page if any.
func paginate[T any](records []T, match func(T) bool, order pageOrder[T], limit int, cursor string, binding CursorBinding) ([]T, string, error) {
	filtered := make([]T, 0, len(records))
	for _, record := range records {
		if match(record) {
			filtered = append(filtered, record)
		}
	}
	slices.SortFunc(filtered, func(left, right T) int {
		return order.compare(order.sortValue(left), order.objectID(left), order.sortValue(right), order.objectID(right))
	})
	start := 0
	if cursor != "" {
		position, err := DecodeCursor(cursor, binding)
		if err != nil {
			return nil, "", err
		}
		start = len(filtered)
		for index, record := range filtered {
			if order.compare(order.sortValue(record), order.objectID(record), position.SortValue, position.ObjectID) > 0 {
				start = index
				break
			}
		}
	}
	end := min(start+limit, len(filtered))
	page := slices.Clone(filtered[start:end])
	if end >= len(filtered) || end <= start {
		return page, "", nil
	}
	last := filtered[end-1]
	next, err := EncodeCursor(binding, CursorPosition{SortValue: order.sortValue(last), ObjectID: order.objectID(last)})
	if err != nil {
		return nil, "", err
	}
	return page, next, nil
}
