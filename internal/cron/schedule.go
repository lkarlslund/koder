// Package cron runs prompts in chats on a schedule. A job names a chat, a
// 5-field cron schedule in the server's local time, and a prompt; when it
// comes due the prompt is queued in the chat, which runs it once any turn
// in progress has finished.
package cron

import (
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Schedule is a parsed 5-field cron expression: minute, hour, day of month,
// month and day of week (0-7, where 0 and 7 are Sunday).
type Schedule struct {
	minute, hour, dom, month, dow uint64
	// domAny and dowAny record a "*" day field: as in Vixie cron, when
	// both day fields are restricted a day matching either one runs.
	domAny, dowAny bool
}

type field struct {
	name     string
	min, max int
}

var fields = [5]field{
	{"minute", 0, 59},
	{"hour", 0, 23},
	{"day of month", 1, 31},
	{"month", 1, 12},
	{"day of week", 0, 7},
}

// Parse reads a 5-field cron expression. Each field is "*", a number, a
// range a-b, or a list of those, each optionally with a step /n.
func Parse(expr string) (Schedule, error) {
	parts := strings.Fields(expr)
	if len(parts) != 5 {
		return Schedule{}, fmt.Errorf("cron expression %q needs 5 fields: minute hour day-of-month month day-of-week", expr)
	}
	var sets [5]uint64
	for i, part := range parts {
		set, err := parseField(part, fields[i])
		if err != nil {
			return Schedule{}, err
		}
		sets[i] = set
	}
	if sets[4]&(1<<7) != 0 {
		sets[4] = sets[4]&^(1<<7) | 1
	}
	return Schedule{
		minute: sets[0], hour: sets[1], dom: sets[2], month: sets[3], dow: sets[4],
		domAny: parts[2] == "*", dowAny: parts[4] == "*",
	}, nil
}

func parseField(text string, f field) (uint64, error) {
	var set uint64
	for item := range strings.SplitSeq(text, ",") {
		rangeText, stepText, hasStep := strings.Cut(item, "/")
		step := 1
		if hasStep {
			n, err := strconv.Atoi(stepText)
			if err != nil || n < 1 {
				return 0, fmt.Errorf("%s step %q must be a positive number", f.name, stepText)
			}
			step = n
		}
		low, high := f.min, f.max
		switch {
		case rangeText == "*":
		case strings.Contains(rangeText, "-"):
			a, b, _ := strings.Cut(rangeText, "-")
			var err error
			if low, err = fieldNumber(a, f); err != nil {
				return 0, err
			}
			if high, err = fieldNumber(b, f); err != nil {
				return 0, err
			}
			if low > high {
				return 0, fmt.Errorf("%s range %q runs backwards", f.name, rangeText)
			}
		default:
			n, err := fieldNumber(rangeText, f)
			if err != nil {
				return 0, err
			}
			low = n
			if !hasStep {
				high = n
			}
		}
		for v := low; v <= high; v += step {
			set |= 1 << v
		}
	}
	return set, nil
}

func fieldNumber(text string, f field) (int, error) {
	n, err := strconv.Atoi(text)
	if err != nil || n < f.min || n > f.max {
		return 0, fmt.Errorf("%s %q must be a number from %d to %d", f.name, text, f.min, f.max)
	}
	return n, nil
}

// searchYears bounds Next, so a schedule such as February 30 that never
// matches does not loop forever.
const searchYears = 5

// Next returns the first minute after t that the schedule matches, in t's
// location, or the zero time when none does within five years.
func (s Schedule) Next(t time.Time) time.Time {
	loc := t.Location()
	t = t.Truncate(time.Minute).Add(time.Minute)
	limit := t.AddDate(searchYears, 0, 0)
	for t.Before(limit) {
		switch {
		case s.month&(1<<uint(t.Month())) == 0:
			t = time.Date(t.Year(), t.Month()+1, 1, 0, 0, 0, 0, loc)
		case !s.dayMatches(t):
			t = time.Date(t.Year(), t.Month(), t.Day()+1, 0, 0, 0, 0, loc)
		case s.hour&(1<<uint(t.Hour())) == 0:
			t = time.Date(t.Year(), t.Month(), t.Day(), t.Hour()+1, 0, 0, 0, loc)
		case s.minute&(1<<uint(t.Minute())) == 0:
			t = t.Add(time.Minute)
		default:
			return t
		}
	}
	return time.Time{}
}

func (s Schedule) dayMatches(t time.Time) bool {
	dom := s.dom&(1<<uint(t.Day())) != 0
	dow := s.dow&(1<<uint(t.Weekday())) != 0
	if s.domAny || s.dowAny {
		return dom && dow
	}
	return dom || dow
}

var intervalPattern = regexp.MustCompile(`^(\d+)\s*([mhd])$`)

// Normalize turns an interval such as 5m, 2h or 1d into a cron expression
// and checks a cron expression. Intervals run from the top of the hour or
// day, so 2h means every second hour on the hour.
func Normalize(spec string) (string, error) {
	spec = strings.Join(strings.Fields(spec), " ")
	if spec == "" {
		return "", errors.New("a schedule is required")
	}
	if match := intervalPattern.FindStringSubmatch(strings.ToLower(spec)); match != nil {
		n, _ := strconv.Atoi(match[1])
		switch {
		case n < 1:
			return "", errors.New("an interval must be at least 1")
		case match[2] == "m" && n > 59:
			return "", errors.New("a minute interval must be 1-59; use hours instead")
		case match[2] == "h" && n > 23:
			return "", errors.New("an hour interval must be 1-23; use days instead")
		case match[2] == "d" && n > 28:
			return "", errors.New("a day interval must be 1-28; use a cron expression")
		}
		switch match[2] {
		case "m":
			return everyN("*", n) + " * * * *", nil
		case "h":
			return "0 " + everyN("*", n) + " * * *", nil
		default:
			return "0 0 " + everyN("*", n) + " * *", nil
		}
	}
	if _, err := Parse(spec); err != nil {
		return "", fmt.Errorf("%w; or use an interval such as 5m, 2h or 1d", err)
	}
	return spec, nil
}

func everyN(all string, n int) string {
	if n == 1 {
		return all
	}
	return all + "/" + strconv.Itoa(n)
}
