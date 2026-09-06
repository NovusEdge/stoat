# Security

## Reporting

Open a private security advisory at
https://github.com/NovusEdge/stoat/security/advisories/new. Do not open a
public issue.

You get an acknowledgement within 7 days and a fix or a decision within
90 days. Public disclosure waits for the fix or the 90 days, whichever
comes first.

## What counts

- A guest reaching host files outside the 9p share it was given.
- An MCP tool that runs host code, or accepts a host path outside
  `~/.stoat/shared/<vm>/`.
- A bundled recipe that pipes a download into a root shell without a
  checksum or a signature check.
- A secret written to a log, a `--json` result, or a world-readable file.

## What does not count

- A guest escaping QEMU. That is QEMU's boundary.
- An agent destroying a VM it was given access to. Snapshots are the
  mitigation.
- Prompt injection of an agent that drives stoat.
