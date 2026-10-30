// Package crontool lets the model schedule prompts in its own chat, once or
// on a recurring cron schedule.
package crontool

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/lkarlslund/koder/internal/cron"
	"github.com/lkarlslund/koder/internal/id"
	"github.com/lkarlslund/koder/internal/textutil"
	"github.com/lkarlslund/koder/internal/tools"
)

const serviceKey = "cron"

// Control schedules, lists and deletes a chat's jobs; *cron.Scheduler
// implements it.
type Control interface {
	Create(ctx context.Context, sessionID, chatID id.ID, spec, prompt string, recurring bool) (cron.Job, error)
	List(ctx context.Context, chatID id.ID) ([]cron.Job, error)
	Delete(ctx context.Context, chatID id.ID, jobID string) (cron.Job, error)
}

// RuntimeService returns the tool runtime service entry for control.
func RuntimeService(control Control) map[string]any {
	if control == nil {
		return nil
	}
	return map[string]any{serviceKey: control}
}

const parameters = `{"type":"object","properties":{` +
	`"action":{"type":"string","enum":["create","list","delete"],"description":"create schedules a prompt; list shows this chat's jobs; delete cancels one."},` +
	`"schedule":{"type":"string","description":"For create: a 5-field cron expression in the server's local time (minute hour day-of-month month day-of-week), e.g. \"3 9 * * 1-5\", or an interval: 5m, 2h or 1d."},` +
	`"prompt":{"type":"string","description":"For create: the prompt to run in this chat when the job comes due. Write it so it makes sense on its own when it arrives later."},` +
	`"recurring":{"type":"boolean","description":"For create: true (the default) runs on every match of the schedule; false runs once at the first match and then deletes the job."},` +
	`"id":{"type":"string","description":"For delete: the job ID from create or list."}` +
	`},"required":["action"],"additionalProperties":false}`

func init() {
	tools.Register(tool{}, tools.ToolSpec{
		Title:       "Cron",
		Description: "Schedule a prompt to run in this chat later, once or on a recurring cron schedule.",
		Usage: "Use for \"remind me at X\", \"at <time>, do Y\" and \"every N minutes/hours\" requests. " +
			"The schedule is 5-field cron in the server's local time, no time zone conversion needed. " +
			"For a one-shot job set recurring=false and pin the minute, hour, day of month and month, e.g. \"30 14 30 10 *\" for 14:30 on October 30. " +
			"For a recurring job use e.g. \"*/5 * * * *\", \"7 * * * *\" (hourly) or \"3 9 * * 1-5\" (weekdays around 9). " +
			"When the time asked for is approximate, pick a minute other than :00 or :30, so jobs do not all reach the model provider at once. " +
			"When a job comes due its prompt is queued in this chat and runs once any turn in progress has finished. " +
			"Each job runs at a fixed offset: a recurring job up to 10% of its period late (at most 15 minutes), a one-shot job on :00 or :30 up to 90 seconds early; tell the user the next run time the result gives, not the minute in the expression. " +
			"Recurring jobs expire after 7 days: they run once more and are deleted; mention this when scheduling one. " +
			"A one-shot job that came due while Koder was not running is not run; the chat is told it was missed. " +
			"Jobs survive restarts. To react the moment a command's output changes, watch it with an exec session instead; cron runs on the clock.",
		Parameters:  parameters,
		ExposeToLLM: true,
	})
}

type tool struct{}

func (tool) ID() tools.ID             { return tools.Cron }
func (tool) BypassesPermission() bool { return true }

func (tool) Preview(req tools.Request) string {
	args := req.Args
	switch args["action"] {
	case "create":
		return strings.TrimSpace(args["schedule"] + ": " + textutil.Ellipsize(strings.Join(strings.Fields(args["prompt"]), " "), 80))
	case "delete":
		return "delete " + args["id"]
	default:
		return args["action"]
	}
}

func (tool) NormalizeArgs(args map[string]string) (map[string]string, error) {
	out := map[string]string{"action": strings.TrimSpace(args["action"])}
	switch out["action"] {
	case "list":
	case "create":
		out["schedule"] = strings.Join(strings.Fields(args["schedule"]), " ")
		out["prompt"] = strings.TrimSpace(args["prompt"])
		if out["schedule"] == "" || out["prompt"] == "" {
			return nil, errors.New("create needs a schedule and a prompt")
		}
		out["recurring"] = "true"
		if raw := strings.TrimSpace(args["recurring"]); raw != "" {
			recurring, err := strconv.ParseBool(raw)
			if err != nil {
				return nil, errors.New("recurring must be true or false")
			}
			out["recurring"] = strconv.FormatBool(recurring)
		}
	case "delete":
		out["id"] = strings.TrimSpace(args["id"])
		if out["id"] == "" {
			return nil, errors.New("delete needs the job id")
		}
	default:
		return nil, errors.New("action must be create, list or delete")
	}
	return out, nil
}

func (tool) Call(ctx context.Context, opts tools.Options) (tools.Result, error) {
	control, err := tools.RequireService[Control](opts.Runtime, serviceKey)
	if err != nil {
		return tools.Result{}, err
	}
	runtime, args := opts.Runtime, opts.Request.Args
	if runtime.SessionID == "" || runtime.ChatID == "" {
		return tools.Result{}, errors.New("cron jobs need a persisted chat")
	}
	switch args["action"] {
	case "create":
		job, err := control.Create(ctx, runtime.SessionID, runtime.ChatID, args["schedule"], args["prompt"], args["recurring"] == "true")
		if err != nil {
			return tools.Result{}, err
		}
		return result(job, createdText(job)), nil
	case "delete":
		job, err := control.Delete(ctx, runtime.ChatID, args["id"])
		if err != nil {
			return tools.Result{}, err
		}
		return result(job, "Deleted cron job "+job.ID+" ("+job.Cron+")."), nil
	default:
		jobs, err := control.List(ctx, runtime.ChatID)
		if err != nil {
			return tools.Result{}, err
		}
		return tools.Result{Output: listText(jobs), Meta: map[string]string{"job_count": strconv.Itoa(len(jobs))}}, nil
	}
}

func result(job cron.Job, text string) tools.Result {
	return tools.Result{Output: text, Meta: map[string]string{"job_id": job.ID, "cron": job.Cron}}
}

func createdText(job cron.Job) string {
	if !job.Recurring {
		return fmt.Sprintf("Scheduled one-shot job %s (%s). It runs at %s, then deletes itself.", job.ID, job.Cron, when(job.NextRunAt))
	}
	return fmt.Sprintf("Scheduled recurring job %s (%s). Next run: %s. It expires %s, after a final run; delete it sooner with the cron tool.",
		job.ID, job.Cron, when(job.NextRunAt), when(job.CreatedAt.Add(cron.RecurringMaxAge)))
}

func listText(jobs []cron.Job) string {
	if len(jobs) == 0 {
		return "This chat has no cron jobs."
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d cron job(s), next run first:\n", len(jobs))
	for _, job := range jobs {
		kind := "one-shot"
		if job.Recurring {
			kind = "recurring"
		}
		fmt.Fprintf(&b, "- %s %s (%s), next run %s: %s\n", job.ID, kind, job.Cron, when(job.NextRunAt), textutil.Ellipsize(strings.Join(strings.Fields(job.Prompt), " "), 200))
	}
	return strings.TrimRight(b.String(), "\n")
}

func when(t time.Time) string {
	return t.Local().Format("Mon 2006-01-02 15:04:05 MST")
}
