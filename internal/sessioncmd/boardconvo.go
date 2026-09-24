package sessioncmd

import (
	"errors"
	"fmt"
	"strings"

	"github.com/usestring/gate-inbox/internal/handover"
	"github.com/usestring/gate-inbox/internal/migrate"
)

// BoardHandoverOptions shapes one BoardHandover.
type BoardHandoverOptions struct {
	// Until ends the copy at the last record whose text holds it, and drops
	// everything after: a rewind to the last turn still on course. Empty
	// copies the whole conversation from its last compaction.
	Until string
	// Dest is where the copy is written; empty is beside the transcript.
	Dest string
}

// HandoverCopy is one filtered transcript, written for a replacement to read.
type HandoverCopy struct {
	// Path is the file to hand over: the filtered copy, or the transcript
	// itself when the filter removed nothing and Until cut nothing.
	Path string
	// Note says what was removed, addressed to the agent reading Path; empty
	// when nothing was.
	Note string
	// Cut says Until matched a record and the copy ends there.
	Cut bool
	// Filtered says Path is a copy with something removed.
	Filtered bool
}

// BoardTranscript is where an agent session's conversation can be read, as a
// migration finds it.
func (s *Sessions) BoardTranscript(targetID string) (transcript migrate.Transcript, err error) {
	defer start("sessioncmd.board.transcript", sessionAttr(targetID)).done(&err)
	return s.locate("", targetID)
}

// Transcript is BoardTranscript for a calling session, which reaches any
// agent session's conversation as a migration does.
func (s *Sessions) Transcript(sessionID, targetID string) (transcript migrate.Transcript, err error) {
	defer start("sessioncmd.transcript", sessionAttr(targetID)).done(&err)
	return s.locate(sessionID, targetID)
}

// locate finds targetID's transcript, checking the caller first unless it is
// the board.
func (s *Sessions) locate(callerID, targetID string) (migrate.Transcript, error) {
	runtime, err := s.open()
	if err != nil {
		return migrate.Transcript{}, err
	}
	defer runtime.store.Close()
	if callerID != "" {
		if _, err := runtime.caller(callerID); err != nil {
			return migrate.Transcript{}, err
		}
	}
	target, err := runtime.agent(targetID)
	if err != nil {
		return migrate.Transcript{}, err
	}
	return migrate.Locate(s.roots, target.Tool, runtime.cfg.Tools[target.Tool], target)
}

// BoardHandover writes the handover filter's copy of an agent session's
// transcript, as a migration hands one over, optionally cut at a quoted
// deviation point. It fails for a layout the filter cannot read rather than
// handing the raw conversation over as filtered.
func (s *Sessions) BoardHandover(targetID string, opts BoardHandoverOptions) (copied HandoverCopy, err error) {
	defer start("sessioncmd.board.handover", sessionAttr(targetID)).done(&err)
	return s.handover("", targetID, opts)
}

// Handover is BoardHandover for a calling session.
func (s *Sessions) Handover(sessionID, targetID string, opts BoardHandoverOptions) (copied HandoverCopy, err error) {
	defer start("sessioncmd.handover", sessionAttr(targetID)).done(&err)
	return s.handover(sessionID, targetID, opts)
}

func (s *Sessions) handover(callerID, targetID string, opts BoardHandoverOptions) (copied HandoverCopy, err error) {
	transcript, err := s.locate(callerID, targetID)
	if err != nil {
		return HandoverCopy{}, err
	}
	if transcript.Path == "" {
		return HandoverCopy{}, fmt.Errorf("session %s keeps its conversation where only a command prints it; the handover filter reads files", targetID)
	}
	filter := handover.DefaultOptions()
	cut := false
	if quote := strings.TrimSpace(opts.Until); quote != "" {
		line, found, err := handover.DeviationCut(transcript.Kind, transcript.Path, quote)
		if err != nil {
			return HandoverCopy{}, err
		}
		if found {
			filter.KeepTo, cut = &line, true
		}
	}
	dst := opts.Dest
	if dst == "" {
		dst = transcript.Path + ".handover.jsonl"
	}
	if dst == transcript.Path {
		return HandoverCopy{}, errors.New("the handover copy cannot overwrite the transcript it filters")
	}
	stats, filtered, err := filterTranscript(transcript, dst, filter)
	if !filtered {
		return HandoverCopy{}, fmt.Errorf("the handover filter cannot read %s transcripts", transcript.Kind)
	}
	if err != nil {
		return HandoverCopy{}, fmt.Errorf("handover filter failed for %s: %w", transcript.Path, err)
	}
	if stats.Empty() && !cut {
		return HandoverCopy{Path: transcript.Path}, nil
	}
	copied = HandoverCopy{Path: dst, Cut: cut, Filtered: true}
	if !stats.Empty() {
		copied.Note = stats.Note()
	}
	return copied, nil
}
