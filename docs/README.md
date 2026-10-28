# git2-deploy docs

Docs for the git2 lanes that run on what the Platform & Backup lane ships: Dash, the management console, codeplane and CI. Each doc is meant to be enough on its own; you shouldn't need to read our code.

| Start here | |
|---|---|
| [version_notes/overview.md](version_notes/overview.md) | What changed, newest first, and what your lane has to do about it |
| [api/](api/) | Reference for the Compose stack, the gateway, `backup.v1` and the snapshot format. Where the docs and the code disagree, this is the source of truth |
| [design/plan.md](design/plan.md) | What this lane owes, week by week, and what is still open (draft) |
| [design/assumptions.md](design/assumptions.md) | What we assume about your repo. Please confirm or correct your rows |

When an update needs work in your lane, Ceyda opens an issue on the git2 repo that links the version note.
