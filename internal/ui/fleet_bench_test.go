// Modified by Durable Alpha, 2026: changes from the upstream commit named in NOTICE.

package ui

import (
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/forge"
	"github.com/usestring/gate-inbox/internal/status"
	"github.com/usestring/gate-inbox/internal/store"
	"github.com/usestring/gate-inbox/internal/sysstat"
	"github.com/usestring/gate-inbox/internal/worktracker"
)

// fleetSize is what this machine actually runs. Every benchmark here is
// sized to it, because the six-session fixture the screenshot test uses
// renders a rail that never scrolls and never rolls a group up over more
// than two sessions, which is not the workload.
const fleetSize = 87

// fleetGroups nests three deep, which is what puts more than one guide slot
// on a row and makes the group rollups walk the whole session list.
var fleetGroups = []string{
	"", "backend", "backend/api", "backend/api/auth", "backend/web",
	"infra", "infra/terraform", "frontend", "data", "data/pipelines",
}

// fleetGroupPaths gives every fixture group a fixed directory so the golden
// carries no path from the machine that recorded it.
func fleetGroupPaths() map[string]string {
	paths := make(map[string]string, len(fleetGroups))
	for _, group := range fleetGroups {
		paths[group] = "/home/user/repos/sample-repo"
		if group != "" {
			paths[group] += "/" + group
		}
	}
	return paths
}

var fleetStatuses = []string{
	status.Working, status.Waiting, status.Finished, status.Errored,
	status.Idle, status.Dead, status.Starting,
}

var fleetTools = []string{"claude", "codex", "grok", "opencode", "claude", "zsh"}

// fleetName alternates a short name with one long enough to be truncated,
// because truncation is width measurement over a styled string and that is
// the work a name column actually does.
func fleetName(i int) string {
	if i%4 == 0 {
		return fmt.Sprintf("abc-13%04d-gate-inbox-render-performance-at-fleet-scale-%d", 5000+i, i)
	}
	return fmt.Sprintf("session-%02d", i)
}

// fleetPR and fleetTicket are the artifacts a session carries. Every third
// session is on something, which is roughly this machine's share.
func fleetPR(i int) int        { return 7000 + i }
func fleetTicket(i int) string { return fmt.Sprintf("ABC-13%04d", 5000+i) }

func fleetHasWork(i int) bool { return i%3 == 0 }

// fleetSessions ages every row in whole hours. relSince reads the wall clock
// with no seam to freeze, so only offsets whose answer cannot drift between
// two runs let one fixture back both the benchmarks and a golden frame.
func fleetSessions(n int) []store.Session {
	now := time.Now()
	sessions := make([]store.Session, 0, n)
	for i := 0; i < n; i++ {
		prompt := "run the suite and report what broke"
		if fleetHasWork(i) {
			prompt = fmt.Sprintf("open PR #%d for %s and post the summary", fleetPR(i), fleetTicket(i))
		}
		sessions = append(sessions, store.Session{
			ID:           "sess-" + strconv.Itoa(i),
			Name:         fleetName(i),
			Group:        fleetGroups[i%len(fleetGroups)],
			Tool:         fleetTools[i%len(fleetTools)],
			Status:       fleetStatuses[i%len(fleetStatuses)],
			Cwd:          "/home/user/repos/worktrees/fleet-" + strconv.Itoa(i) + "/sample-repo",
			CreatedAt:    now.Add(-time.Duration(i+1) * time.Hour),
			LastStatusAt: now.Add(-time.Duration(i+1) * time.Hour),
			LaunchPrompt: prompt,
		})
	}
	return sessions
}

// fleetWork resolves every artifact the fixture's sessions mention, so the
// rail draws real fold marks, real badges and real artifact rows rather than
// the empty column a tracker-less model draws.
func fleetWork(n int) *worktracker.Tracker {
	prs := map[string]forge.PR{}
	tickets := map[string]forge.Ticket{}
	for i := 0; i < n; i++ {
		if !fleetHasWork(i) {
			continue
		}
		number := fleetPR(i)
		checks := []forge.ChecksState{forge.ChecksPassing, forge.ChecksFailing, forge.ChecksPending}[i%3]
		review := forge.ReviewApproved
		if i%5 == 0 {
			review = forge.ReviewChangesRequested
		}
		prs["pr:example-org/sample-repo#"+strconv.Itoa(number)] = forge.PR{
			Repo: "example-org/sample-repo", Number: number, State: forge.PROpen,
			Checks: checks, Review: review, Mergeable: i%2 == 0,
			URL: "https://github.com/example-org/sample-repo/pull/" + strconv.Itoa(number),
		}
		id := fleetTicket(i)
		tickets["ticket:"+id] = forge.Ticket{
			Identifier: id, State: []string{"In Review", "In Progress", "Todo"}[i%3],
			StateType: []string{"started", "started", "unstarted"}[i%3],
			URL:       "https://linear.app/example/issue/" + id,
		}
	}
	return worktracker.New(
		fakeGit{remote: "git@github.com:example-org/sample-repo.git"},
		fakePRs{prs: prs, health: forge.Health{OK: true}},
		fakeTickets{tickets: tickets, health: forge.Health{OK: true}},
	)
}

// fleetModel is the rail as this machine has it: n sessions over nested
// groups, every status in play, a third of them on pull requests and
// tickets, a few groups folded and a few sessions' work opened.
func fleetModel(tb testing.TB, n, width, height int) *Model {
	tb.Helper()
	sessions := fleetSessions(n)
	m := &Model{
		width: width, height: height, mode: modeList,
		sessions:  sessions,
		collapsed: map[string]bool{},
		groups:    fleetGroups[1:],
		// Every group is pinned: an unset one falls back to groupDefaultDir,
		// which reads the process working directory and bakes the recording
		// checkout's path into the golden, so it only matches there.
		groupPaths: fleetGroupPaths(),
		split:      splitState{ratio: defaultSplitRatio},
		work:       fleetWork(n),
		agents:     agentStats{count: n, cpu: 61, ram: 44, rss: 18_530_000_000},
		net:        netStats{rates: true, down: 9_400_000, up: 2_100_000},
		snap: sysstat.Snapshot{
			CPUOK: true, CPUPercent: 88,
			MemOK: true, MemPercent: 75, MemUsed: 12_100_000_000, MemTotal: 16_000_000_000,
			SwapOK: true, SwapPercent: 43, SwapUsed: 4_500_000_000, SwapTotal: 8_000_000_000, SwapCeiling: 8_000_000_000,
			DiskOK: true, DiskPercent: 88, DiskUsed: 400_000_000_000, DiskFree: 100_000_000_000, DiskTotal: 500_000_000_000,
			CPUTempOK: true, CPUTemp: 61, GPUTempOK: true, GPUTemp: 55,
		},
		preview:      previewSample,
		proc:         sysstat.ProcStat{OK: true, CPUPercent: 4.2, RamPercent: 3.6, RSS: 612_000_000},
		firstPrompts: map[string][]string{},
	}
	m.cfg.Tools = map[string]config.Tool{"zsh": {Shell: true}}
	// The tracker only knows what a discovery pass told it, and that pass is
	// the command refreshWork hands back rather than refreshWork itself.
	if cmd := m.refreshWork(); cmd != nil {
		cmd()
	}
	// Half the sessions on something have their artifacts showing, which is
	// what puts artifact rows in the tree and drops the badge from the rows
	// that carry them.
	for i := 0; i < n; i++ {
		if fleetHasWork(i) && i%6 == 0 {
			m.setWorkFolded("sess-"+strconv.Itoa(i), false)
		}
	}
	m.collapsed["data/pipelines"] = true
	m.collapsed["infra/terraform"] = true
	m.rebuildRows()
	m.placeCursor(len(m.rows) / 2)
	if sess, ok := m.selected(); ok {
		m.procFor = sess.ID
	}
	return m
}

// placeCursor is a cursor put on a row rather than stepped onto it, which is
// what a test and the golden do. The tree settles around it the same way,
// because which session's work is open is a fact about the cursor.
func (m *Model) placeCursor(index int) *Model {
	m.cursor = index
	m.rebuildRows()
	return m
}

// benchSize is a terminal the rail has to fit: a narrow one where the
// columns fight, the common one, and a wide one where the frame paints the
// most cells.
type benchSize struct {
	name          string
	width, height int
}

var benchSizes = []benchSize{
	{"40x50", 40, 50},
	{"120x34", 120, 34},
	{"200x50", 200, 50},
}

// benchCounts pairs the old fixture's size with this machine's, so a result
// says whether a cost scales with the fleet or only with the terminal.
var benchCounts = []int{6, fleetSize}

func BenchmarkFleetFrame(b *testing.B) {
	for _, n := range benchCounts {
		for _, size := range benchSizes {
			b.Run(fmt.Sprintf("n=%d/%s", n, size.name), func(b *testing.B) {
				m := fleetModel(b, n, size.width, size.height)
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					_ = m.frame()
				}
			})
		}
	}
}

// BenchmarkFleetComfortable is the two-line density, which doubles the
// stacked rows the rail paints and halves how many fit.
func BenchmarkFleetComfortable(b *testing.B) {
	for _, n := range benchCounts {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			m := fleetModel(b, n, 120, 34)
			m.comfortableRows = true
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = m.frame()
			}
		})
	}
}

func BenchmarkFleetRail(b *testing.B) {
	for _, n := range benchCounts {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			m := fleetModel(b, n, 200, 50)
			left, _ := m.splitWidths()
			height := m.listBodyHeight()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = m.railLines(left-1, height)
			}
		})
	}
}

func BenchmarkFleetContent(b *testing.B) {
	for _, n := range benchCounts {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			m := fleetModel(b, n, 200, 50)
			_, right := m.splitWidths()
			height := m.listBodyHeight()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = m.contentLines(right-2, height)
			}
		})
	}
}

func BenchmarkFleetRebuildRows(b *testing.B) {
	for _, n := range benchCounts {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			m := fleetModel(b, n, 200, 50)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				m.rebuildRows()
			}
		})
	}
}

func BenchmarkFleetTriageSort(b *testing.B) {
	for _, n := range benchCounts {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			source := fleetSessions(n)
			queue := make([]store.Session, len(source))
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				copy(queue, source)
				(&Model{}).sortTriage(queue)
			}
		})
	}
}

// BenchmarkFleetTriageFrame renders the mode the sort feeds, since triage
// flattens every session into one rail and is where a long list is longest.
func BenchmarkFleetTriageFrame(b *testing.B) {
	for _, n := range benchCounts {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			m := fleetModel(b, n, 200, 50)
			m.triage = true
			m.rebuildRows()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = m.frame()
			}
		})
	}
}

// BenchmarkFleetTriageScopedFrame is the same mode narrowed to one group's
// subtree, which is the shape a real drain has. It pays the scope check on
// every group and the badge and footer that name the group, over a rail with
// fewer rows to paint.
func BenchmarkFleetTriageScopedFrame(b *testing.B) {
	for _, n := range benchCounts {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			m := fleetModel(b, n, 200, 50)
			m.triage = true
			m.triageScope = "backend"
			m.rebuildRows()
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = m.frame()
			}
		})
	}
}

// BenchmarkFleetScroll is what "smooth" means to somebody holding j down:
// one cursor step and the frame that step has to paint.
func BenchmarkFleetScroll(b *testing.B) {
	for _, n := range benchCounts {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			m := fleetModel(b, n, 200, 50)
			// A step clears the preview, so the frames a held key paints are
			// the ones with nothing captured yet.
			m.preview = ""
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				m.moveCursor(1)
				_ = m.frame()
			}
		})
	}
}

// BenchmarkFleetKeystroke is a held key as Bubble Tea sees it. A cursor move
// arms a preview settle that the next keystroke supersedes, tea.Tick cannot be
// cancelled, and Bubble Tea paints after every message -- so one keystroke is
// two messages and was two full frames. Both are driven here through the real
// handlers, because which of them is a paint for nothing is the point.
func BenchmarkFleetKeystroke(b *testing.B) {
	for _, n := range benchCounts {
		b.Run(fmt.Sprintf("n=%d", n), func(b *testing.B) {
			m := fleetModel(b, n, 200, 50)
			// A step clears the preview, so the frames a held key paints are
			// the ones with nothing captured yet.
			m.preview = ""
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				m.moveCursor(1)
				_ = m.frame()
				m.update(previewSettleMsg{gen: m.previewGen - 1})
				_ = m.frame()
			}
		})
	}
}
