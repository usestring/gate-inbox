package mcpreg

// claudeSteering is all a claude launch appends to the system prompt. The
// delegation and child-dialog steering itself is the Gate Inbox plugin's
// delegate-with-gate-inbox skill (claudeplugin), read only when a session is
// about to delegate; this line is what makes it get read.
//
// Measured on claude 2.1.296 with a stub gate-inbox server and a prompt
// asking for a subagent, three runs per arm: the full appended steering sent
// opus 5.5 and sonnet 5.5 to create_session 6 of 6 times; the skill with
// nothing appended, 2 of 6 (sonnet took the Agent tool every time); the
// skill with this line, 6 of 6, at 578 fewer first-turn tokens than the full
// steering. Nothing at all: 0 of 6.
//
// Rule 2 is not repeated here: a claude child gets it from the SessionStart
// hook (sessioncmd.ChildApprovalNote), and only a child needs it.
//
// The report_status line is what lets the board tell a finished session
// from one that only stopped, without guessing from its last few turns.
const claudeSteering = "Gate Inbox manages this session. Before handing work to another agent " +
	"(Agent, Task or Workflow tool), load the gate-inbox:delegate-with-gate-inbox skill: " +
	"real work goes to create_session sessions on the user's board.\n" +
	"End every turn by calling the gate-inbox report_status tool with done, waiting, blocked or working.\n"
