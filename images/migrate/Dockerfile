# syntax=docker/dockerfile:1
#
# migrate: a one-shot job that applies the SQL files in the git2 repo's
# migrations/ with that repo's own helpers/dev/migrate.sh. Every service's
# schema lives there (team rule), so this one job migrates the whole instance.
#
# migrate.sh records applied files in public.schema_migrations and runs each
# file in its own transaction, so running the job on every start is safe.
#
# The source comes from the named build context "src" (see
# compose/compose.yaml). The Postgres image is used only for psql and bash;
# no server runs in this container.

ARG POSTGRES_VERSION=18

FROM postgres:${POSTGRES_VERSION}-alpine
LABEL org.opencontainers.image.source="https://github.com/git2-project/git2" \
      org.opencontainers.image.description="git2 database migrations"
COPY --from=src migrations/ /git2/migrations/
COPY --from=src helpers/dev/migrate.sh helpers/dev/env.sh /git2/helpers/dev/
USER postgres
ENTRYPOINT ["/git2/helpers/dev/migrate.sh"]
