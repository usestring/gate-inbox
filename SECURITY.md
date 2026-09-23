# Security policy

## Reporting a vulnerability

Report vulnerabilities privately through GitHub:
open the repository's **Security** tab and choose **Report a vulnerability**. The report stays
private between you and the maintainers until a fix is published.

Please do not open a public issue, discussion or pull request for a security problem.

A useful report includes:

- the version (`gate-inbox --version`) or commit, and your OS and tmux version
- steps to reproduce, or a proof of concept
- what an attacker gains, and what access they need first

We will acknowledge the report, confirm or dispute it, and agree a disclosure date with you. When a
report is confirmed, you will be credited in the advisory unless you ask not to be.

## Supported versions

Fixes land on `main` and in the next release. Older releases are not patched.

## Scope

Gate Inbox starts the agent CLIs you already have installed, with your user's permissions, and does
not sandbox them. What an agent does inside its own session belongs to that agent's own security
policy. A report is in scope here when Gate Inbox itself crosses a boundary: for example, it runs
something you did not ask for, exposes a session or its transcript to another user, or leaks a
credential into its logs or state.

Gate Inbox is a fork of the upstream project named in [`NOTICE`](NOTICE). If a
vulnerability is in code still shared with upstream, we will tell upstream too.
