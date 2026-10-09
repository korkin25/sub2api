# Fork maintenance

This fork tracks [Wei-Shaw/sub2api](https://github.com/Wei-Shaw/sub2api).

- `main` tracks upstream application code and retains this fork's Helm chart
  and GHCR publishing workflow.
- `integration/reset-policies` combines upstream with the additional reset
  policies, quota recovery, and OAuth WebSocket creation defaults. Updating
  either branch does not update a deployment pinned to an image digest.

The October 2026 refresh uses upstream `3a6fd1c9db07203ca308aaba69e502bc1f35b307`
(v0.2.15 application code; the following commit only updates VERSION).

## Patch ownership

Use upstream's HTTP Responses beta-header forwarding (#7617), Claude reset
status (#7684, replacing #7589), and manual Claude reset redemption (#7726,
adapting #7591). Do not reintroduce their old fork implementations.

The remaining upstream proposals are:

| PR | Behavior | Dependency |
| --- | --- | --- |
| [#7590](https://github.com/Wei-Shaw/sub2api/pull/7590) | Reset only after the eligible account cohort is exhausted | upstream |
| [#7592](https://github.com/Wei-Shaw/sub2api/pull/7592) | Redeem expiring credits only with a measured quota benefit | #7590 |
| [#7593](https://github.com/Wei-Shaw/sub2api/pull/7593) | Separate provider policies and Claude automation | #7590 and #7592; upstream native Claude redemption |
| [#7627](https://github.com/Wei-Shaw/sub2api/pull/7627) | Configurable WSv2 default for newly created OAuth accounts | upstream |

Global policy enforcement and recovery of stale account cooldowns remain
integration-specific. Upstream's manual reset implementation changed its
durable fence format and scope; upgrades from older fork images must preserve
unresolved redemption records rather than treating them as new operations.

Refresh stacked proposals in dependency order, preserve upstream quota-query
backoff, and verify provider/group/model isolation and uncertain redemption
outcomes. Keep packaging changes out of upstream feature PRs.
