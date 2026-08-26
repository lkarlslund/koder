package store

import (
	"slices"

	"github.com/lkarlslund/koder/internal/memory"
)

type EntryDeletionBlockers struct {
	LinkIDs            []memory.LinkID  `json:"link_ids,omitempty"`
	SupersededEntryIDs []memory.EntryID `json:"superseded_entry_ids,omitempty"`
}

func (b EntryDeletionBlockers) Empty() bool {
	return len(b.LinkIDs) == 0 && len(b.SupersededEntryIDs) == 0
}

func DeriveEntryDeletionBlockers(target memory.EntryID, entries []memory.Entry, links []memory.Link) EntryDeletionBlockers {
	blockers := EntryDeletionBlockers{}
	for _, entry := range entries {
		if entry.ID != target && entry.SupersededByID == target {
			blockers.SupersededEntryIDs = append(blockers.SupersededEntryIDs, entry.ID)
		}
	}
	for _, link := range links {
		if (link.Source.Kind == memory.ObjectKindEntry && link.Source.ID == string(target)) ||
			(link.Target.Kind == memory.ObjectKindEntry && link.Target.ID == string(target)) {
			blockers.LinkIDs = append(blockers.LinkIDs, link.ID)
		}
	}
	slices.Sort(blockers.LinkIDs)
	slices.Sort(blockers.SupersededEntryIDs)
	return blockers
}
