package status

import (
	"regexp"
	"strings"
	"time"
)

var resetPhrase = regexp.MustCompile(`(?i)try again at\s+(.+)`)
var ordinalDay = regexp.MustCompile(`(\d)(?:st|nd|rd|th)\b`)
var meridiemSpace = regexp.MustCompile(`(?i)\s+(am|pm)\b`)

// Clock-only banners omit the date. Resolve it once at observation time;
// parsing on every poll would move tomorrow's deadline forward forever.
func LimitReset(banner string, observed time.Time) (time.Time, bool) {
	if !strings.Contains(banner, "You've hit your ") {
		return time.Time{}, false
	}
	match := resetPhrase.FindStringSubmatch(banner)
	if match == nil {
		return time.Time{}, false
	}
	stamp := match[1]
	loc := observed.Location()
	stamp = strings.TrimSuffix(strings.TrimSpace(stamp), ".")
	stamp = ordinalDay.ReplaceAllString(stamp, "$1")
	stamp = strings.ReplaceAll(stamp, " at ", " ")
	stamp = strings.ReplaceAll(stamp, ",", "")
	stamp = meridiemSpace.ReplaceAllString(stamp, "$1")
	stamp = strings.ReplaceAll(strings.ReplaceAll(stamp, "AM", "am"), "PM", "pm")
	for _, layout := range []string{"Jan 2 2006 3:04pm", "Jan 2 2006 3pm", "2 Jan 2006 15:04", "Jan 2 2006 15:04"} {
		if at, err := time.ParseInLocation(layout, stamp, loc); err == nil {
			return at, true
		}
	}
	local := observed.In(loc)
	for _, layout := range []string{"Jan 2 3:04pm", "Jan 2 3pm", "Jan 2 15:04", "3:04pm", "3pm", "15:04"} {
		parsed, err := time.ParseInLocation(layout, stamp, loc)
		if err != nil {
			continue
		}
		month, day := local.Month(), local.Day()
		hasDate := strings.HasPrefix(layout, "Jan")
		if hasDate {
			month, day = parsed.Month(), parsed.Day()
		}
		at := time.Date(local.Year(), month, day, parsed.Hour(), parsed.Minute(), 0, 0, loc)
		if at.Add(time.Minute).Before(observed) {
			if hasDate {
				at = at.AddDate(1, 0, 0)
			} else {
				at = at.AddDate(0, 0, 1)
			}
		}
		return at, true
	}
	return time.Time{}, false
}
