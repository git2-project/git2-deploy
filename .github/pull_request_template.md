## What and why

<!-- One or two sentences. Link the issue or the version note if there is one. -->

## Checklist

- [ ] `make tests` and `make lint` pass
- [ ] Can anyone outside this repo notice this change (image, port, setting, volume, contract, event)?
  If yes: `docs/api/` is updated, a version note is added under `docs/version_notes/`, and its line is at the top of `docs/version_notes/overview.md`
- [ ] Contract change (`proto/backup/v1/`, `docs/api/gateway.md`)? `make proto` was run, and every affected owner approved
- [ ] Needs a table or column? Migration PR on git2: <!-- link -->
- [ ] No secrets, tokens, DSNs or `.env` in the diff
