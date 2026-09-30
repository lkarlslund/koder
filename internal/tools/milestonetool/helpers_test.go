package milestonetool

import "github.com/lkarlslund/koder/internal/planning"

func upsertMilestone(existing []planning.Milestone, next planning.Milestone) []planning.Milestone {
	out := append([]planning.Milestone(nil), existing...)
	for idx := range out {
		if planning.MilestoneKey(out[idx]) != planning.MilestoneKey(next) {
			continue
		}
		next.Position = out[idx].Position
		out[idx] = next
		return out
	}
	next.Position = len(out)
	return append(out, next)
}
