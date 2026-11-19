package cli

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/usestring/gate-inbox/internal/config"
	"github.com/usestring/gate-inbox/internal/logging"
	"github.com/usestring/gate-inbox/internal/mcpserver"
	"github.com/usestring/gate-inbox/internal/sessioncmd"
)

// piBoardHook answers the `hook pi <verb>` calls of the board's pi extension
// that ask the board for something rather than report a status event, and
// says whether verb was one of them. Like every hook it fails quietly: an
// error answers nothing, and the extension reads nothing as "carry on".
func piBoardHook(verb string, in io.Reader, out io.Writer, sessionID, configDir string) bool {
	switch verb {
	case "steering":
		// What before_agent_start adds to the system prompt. Pi shows the
		// model one line of the server's own instructions, so the board's
		// standing rules have to arrive this way.
		writeJSON(out, map[string]string{"steering": mcpserver.PiInstructions(configDir)})
	case "session_info_changed":
		// Pi's own /name, or a name the extension set from a rename tool
		// call, mirrored onto the board row. Renaming to the name the row
		// already carries is harmless, so the echo needs no special case.
		var p struct {
			Name string `json:"name"`
		}
		if json.NewDecoder(io.LimitReader(in, 1<<16)).Decode(&p) == nil && p.Name != "" && sessionID != "" {
			_, _ = sessioncmd.Rename(configDir, sessionID, p.Name)
		}
	case "mcp_override":
		// A gate-inbox server in the operator's own pi config wins over
		// the extension's registration, so the session's board tools are
		// whatever that entry starts. The extension says so in the pane;
		// this puts it in the board's log as well.
		var p struct {
			Sources []string `json:"sources"`
		}
		if json.NewDecoder(io.LimitReader(in, 1<<16)).Decode(&p) == nil && len(p.Sources) > 0 {
			logBoardWarning(configDir, "a pi config defines its own gate-inbox MCP server, which overrides the board's",
				"session", sessionID, "sources", p.Sources)
		}
	default:
		return false
	}
	return true
}

func writeJSON(out io.Writer, value any) {
	if data, err := json.Marshal(value); err == nil {
		fmt.Fprintln(out, string(data))
	}
}

// logBoardWarning appends one warning to the board's log from a hook
// process, which otherwise logs nothing: only the board opens the log.
func logBoardWarning(configDir, msg string, args ...any) {
	settings := logging.Settings{}
	if cfg, err := config.LoadDir(configDir); err == nil {
		settings = logging.Settings{Level: cfg.Log.Level, File: cfg.Log.File, MaxSizeMB: cfg.Log.MaxSizeMB,
			MaxBackups: cfg.Log.MaxBackups, MaxTotalMB: cfg.Log.MaxTotalMB, NoCompress: cfg.Log.NoCompress}
	}
	opts, err := logging.Resolve(configDir, settings)
	if err != nil {
		opts.Level = logging.LevelInfo
	}
	logger, err := logging.Open(opts)
	if err != nil {
		return
	}
	defer logger.Close()
	logger.Log(logging.LevelWarn, msg, args...)
}
