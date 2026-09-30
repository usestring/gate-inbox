package hogs

import (
	"fmt"
	"strings"
	"time"
)

// Subject labels every notice, so a firmer one supersedes a softer one still
// waiting in the session's queue rather than stacking behind it.
const Subject = "resource-hog"

// Highest is the firmest tier among alerts.
func Highest(alerts []Alert) Tier {
	top := TierNone
	for _, a := range alerts {
		top = max(top, a.Tier)
	}
	return top
}

// Compose words one notice for every alert a session raised in one sample.
// CPU and memory episodes are tracked apart but told together: two notices
// back to back would be two turns spent on one problem.
func Compose(alerts []Alert, usage Usage, host Host) string {
	var b strings.Builder
	for i, a := range alerts {
		if i > 0 {
			b.WriteString("\n\n")
		}
		switch a.Kind {
		case KindCPU:
			writeCPU(&b, a, usage)
		case KindMemory:
			writeMemory(&b, a, usage, host)
		}
	}
	b.WriteString("\n\n")
	b.WriteString(ask(Highest(alerts), alerts))
	return b.String()
}

func writeCPU(b *strings.Builder, a Alert, usage Usage) {
	fmt.Fprintf(b, "CPU %s: the processes under this session are using %s of CPU (%.1f cores), over %s for %s.",
		a.Tier, percent(usage.CPUPercent), usage.CPUPercent/100, percent(a.Rule.CPUPercent), duration(a.Held))
	if len(usage.TopCPU) > 0 {
		b.WriteString(" Heaviest:")
		for _, p := range usage.TopCPU {
			fmt.Fprintf(b, "\n- pid %d, %s CPU: %s", p.PID, percent(p.CPUPercent), p.Command)
		}
	}
}

func writeMemory(b *strings.Builder, a Alert, usage Usage, host Host) {
	fmt.Fprintf(b, "Memory %s: the processes under this session hold %s", a.Tier, bytesGiB(usage.MemBytes))
	var why []string
	if a.Rule.MemBytes > 0 {
		why = append(why, "over "+bytesGiB(a.Rule.MemBytes))
	}
	if a.Rule.GrowthBytesPerMin > 0 {
		why = append(why, fmt.Sprintf("growing by at least %s a minute", bytesGiB(uint64(a.Rule.GrowthBytesPerMin))))
	}
	if a.Rule.HostAvailableBelow > 0 {
		why = append(why, fmt.Sprintf("while the machine has under %.0f%% of its memory available", a.Rule.HostAvailableBelow))
	}
	if len(why) > 0 {
		b.WriteString(", " + strings.Join(why, ", "))
	}
	if a.Held > 0 {
		fmt.Fprintf(b, ", for %s", duration(a.Held))
	}
	b.WriteString(".")
	if host.OK {
		fmt.Fprintf(b, " The machine has %s of %s available (%.0f%%).",
			bytesGiB(host.MemAvailable), bytesGiB(host.MemTotal), host.AvailablePercent())
	}
	if len(usage.TopMem) > 0 {
		b.WriteString(" Largest:")
		for _, p := range usage.TopMem {
			fmt.Fprintf(b, "\n- pid %d, %s: %s", p.PID, bytesGiB(p.MemBytes), p.Command)
		}
	}
}

// ask is what the session is asked to do, firmest tier first.
func ask(tier Tier, alerts []Alert) string {
	caps := capHint(alerts)
	switch tier {
	case TierStop:
		return "Stop the named processes now: kill them (and any process group or background job that restarts them), " +
			"then say in your reply what you stopped and why it ran away. The machine is shared with other sessions, " +
			"and at this level it is close to being unusable for them. If one of them is the deliverable of your task, " +
			"stop it anyway and restart it capped (" + caps + ")."
	case TierWarn:
		return "Stop or cap the named processes now, unless they are the deliverable of your task. " +
			"If they are, restart them capped (" + caps + ") and say so in your reply; if they are not, kill them."
	default:
		return "Confirm this work is intended. If it is, cap it (" + caps + "); if it is not, stop it. " +
			"Nothing needs to be sent back: act on it and carry on."
	}
}

func capHint(alerts []Alert) string {
	var hints []string
	for _, a := range alerts {
		switch a.Kind {
		case KindCPU:
			hints = append(hints, "`systemd-run --user --scope -p CPUQuota=200% <command>`, or `renice -n 19 -p <pid>` for one already running")
		case KindMemory:
			hints = append(hints, "`systemd-run --user --scope -p MemoryMax=8G <command>`, or a smaller worker count")
		}
	}
	return strings.Join(hints, "; ")
}

func percent(p float64) string { return fmt.Sprintf("%.0f%%", p) }

func bytesGiB(n uint64) string {
	const gib = 1 << 30
	if n < gib {
		return fmt.Sprintf("%.0f MiB", float64(n)/(1<<20))
	}
	return fmt.Sprintf("%.1f GiB", float64(n)/gib)
}

func duration(d time.Duration) string {
	if d < time.Minute {
		return d.Round(time.Second).String()
	}
	return strings.TrimSuffix(d.Round(time.Minute).String(), "0s")
}
