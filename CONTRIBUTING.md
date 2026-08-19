# Contributing

Radar accepts small, evidence-backed changes that preserve its discovery,
identity, and delivery guarantees.

## Before editing

1. Read [docs/README.md](docs/README.md) and the nearest `AGENTS.md`.
2. Check `git status --short` and preserve unrelated work.
3. Reproduce the behavior or identify the invariant being changed.
4. Choose the narrowest package boundary that owns the behavior.

## Development loop

1. Add or update the smallest focused test.
2. Implement the change without weakening failure isolation or delivery safety.
3. Run the narrow test, then the verification level in
   [docs/development.md](docs/development.md).
4. Review `git diff --check` and stage exact paths only.
5. Keep each commit coherent and green. Do not mix generated artifacts,
   unrelated cleanup, or secrets into a feature commit.

## Pull request evidence

Explain:

- what observable behavior changed and why;
- which product invariant or failure mode is affected;
- the exact commands and tests that passed;
- whether schema, configuration, source coverage, or delivery behavior changed;
- whether any external messages were sent.

Persistence and pipeline changes should include measured source, job, identity,
and delivery counts when a live or disposable-database run was performed.

## External effects

Local tests and previews use `RADAR_LITE_DELIVERY_MODE=log` with Telegram
credentials absent. Enabling Telegram, changing production configuration,
deploying, or pushing commits requires explicit user authorization.
