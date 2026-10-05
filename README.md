# mend

**Repair a Postgres database after a bad action (an agent's mistake, an attacker, a bad migration) without throwing away the legitimate work that happened since.**

Point-in-time restore loses everything after the mistake. Reverting the bad transaction leaves later work that *read* the bad data silently wrong. mend re-runs exactly that later work against the repaired data, and keeps everything else. It is a modern take on RETRO (OSDI 2010) and Warp (SOSP 2011).

> **Status: pre-alpha.** Only the evaluation harness exists. There is no repair engine yet.

## Why

Agents with write access to production databases are already causing incidents. In April 2026 a Cursor agent [deleted PocketOS's production database and its backups](https://decrypt.co/365897/ai-agent-deletes-startup-database-9-seconds-founder-says) with a single API call, and in February 2026 a Claude Code session [ran `terraform destroy` against DataTalks.Club's production stack](https://aihola.com/article/claude-code-wipes-production-database). Those were outright deletions, which backups can eventually fix. The harder case is a wrong write that is noticed late, after real work has been built on top of it.

### [The pricing-bot scenario](internal/harness/workload.go#L27-L40)

This scenario is made up to measure that case. An agent asked for "10% off garden products" divides their prices by ten. The shop keeps taking orders. The harness replays the same 4,000 seeded requests in several worlds and compares each with the clean one, where the bad action never happened:

| Strategy | Products missing | Products extra | Products wrong | Orders missing | Orders extra | Orders wrong |
|---|--:|--:|--:|--:|--:|--:|
| No repair | 0 | 0 | 50 | 0 | 0 | 504 |
| Point-in-time restore | 0 | 0 | 200 | 2,122 | 0 | 0 |
| Revert bad action only | 0 | 0 | 0 | 0 | 0 | 504 |

- **Point-in-time restore** loses 2,122 legitimate orders.
- **Reverting the bad action** fixes the prices, but 504 orders priced from them stay wrong, because their totals were computed in application code.
- **mend's target** is every count zero.

These come from one run of a toy workload (`just harness`). They show the shape of the problem, not a benchmark.

## Try the harness

Needs Go 1.27, Docker and [just](https://github.com/casey/just).

```sh
just harness            # starts a scratch Postgres, runs the scenario
just harness -ops 8000 -bad-at 500
just test               # unit tests
just test-integration   # plus tests against Postgres
```

The JSON report lands in `results/pricing-bot.json`.

## Layout

| Path | What |
|---|---|
| `demo/shop` | a small Go web shop; order totals are computed in Go on purpose |
| `internal/harness` | seeded workload, scratch databases, snapshots, diffs, baselines |
| `cmd/mend-harness` | runs a scenario and prints the comparison |

## License

Apache-2.0, see [LICENSE](LICENSE).
