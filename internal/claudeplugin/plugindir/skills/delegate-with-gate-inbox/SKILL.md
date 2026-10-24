---
name: delegate-with-gate-inbox
description: Use before handing any real work (an investigation, implementation, review or multi-step search) to another agent, instead of the Agent, Task or Workflow tool, and whenever a session you created stops on a question or permission dialog. Covers create_session, list_sessions, send_session, wait_for_session and answer_session.
---

# Delegating work: use Gate Inbox sessions

Hand any unit of real work (an investigation, implementation, review or multi-step search) to a Gate Inbox session with create_session instead of using the built-in Agent tool (also called Task: general-purpose, Explore, Plan, fork and custom agents) or the Workflow tool. A session is on the user's board, and its questions are relayed to you. Keep the built-in tool for a quick read-only lookup.

Call list_sessions first and reuse an idle session. Give create_session a name and a prompt that states the whole task; the new agent cannot see this conversation. Track shared plans with the task tool. Check on it with read_session, redirect it with send_session, block on it with wait_for_session, and archive_session when done. One session per workstream.

A top-level session (one with no parent) launches a new session only when its user says so. Unless their latest message tells you to launch one, create_session refuses with an Approval question headed "Approval": ask it with your own question tool word for word, call create_session again with the same name once they choose "Launch new session", and do the work yourself if they choose "Don't launch". Work that merely could run in parallel is not a request to launch. Sessions you spawned are not asked about their own spawns.

If the gate-inbox tools are missing, use the built-in Agent tool as usual.

# Your children's dialogs are yours

When a session you created stops on a dialog, Gate Inbox forwards it to you. Answer what your brief or your user's standing decisions settle. Ask your user the rest with your own question tool, copying each question and its options word for word, then reply with answer_session, marking the answer as your user's own. Permission prompts, trust dialogs and questions headed Approval are always your user's to answer. Never tell your user to answer at the child's pane, and never leave a child waiting unmentioned.
