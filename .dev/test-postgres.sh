#!/usr/bin/env bash
# Starts and removes a disposable PostgreSQL instance for the integration
# tests. See README.md, "Integration tests".
#
#   .dev/test-postgres.sh up [--fixtures]   start an instance, print its state file
#   .dev/test-postgres.sh down STATE_FILE   remove that instance and its state file
#
# The instance listens on a random loopback port, keeps its data in tmpfs and
# is labelled sqlext.test-postgres=<id>. `down` removes only the container
# recorded in the state file. --fixtures applies .dev/migrations, which need an
# image with PostGIS (set SQLEXT_TEST_POSTGRES_IMAGE to a pinned image).
set -euo pipefail

# postgres:16-alpine (16.16), pinned by index digest.
DEFAULT_IMAGE=docker.io/library/postgres@sha256:f2f98726820d0342055351c6e9d36e705f5bfffb3dd53ff556325114b08afb6d
IMAGE=${SQLEXT_TEST_POSTGRES_IMAGE:-$DEFAULT_IMAGE}
LABEL=sqlext.test-postgres
MIGRATIONS=$(cd "$(dirname "$0")" && pwd)/migrations

die() {
	printf 'test-postgres: %s\n' "$*" >&2
	exit 1
}

random_hex() { od -An -N"$1" -tx1 /dev/urandom | tr -d ' \n'; }

# Removed on any exit from `up` unless it succeeds: a half-started container
# and the files holding its password.
cleanup_container=""
cleanup_files=()
cleanup() {
	if [ -n "$cleanup_container" ]; then
		docker rm --force "$cleanup_container" >/dev/null 2>&1 || true
	fi
	if [ "${#cleanup_files[@]}" -gt 0 ]; then
		rm -f "${cleanup_files[@]}"
	fi
}

up() {
	local fixtures=false
	case "${1:-}" in
	"") ;;
	--fixtures) fixtures=true ;;
	*) die "unknown option: $1" ;;
	esac

	local id user db password cluster
	id=$(random_hex 6)
	cluster=sqlext-test-$id
	db=sqlext_test_$id
	user=sqlext_test
	password=$(random_hex 24)

	local secrets state
	trap cleanup EXIT
	secrets=$(mktemp "${TMPDIR:-/tmp}/sqlext-test-postgres-env.XXXXXX")
	state=$(mktemp "${TMPDIR:-/tmp}/sqlext-test-postgres-state.XXXXXX")
	cleanup_files=("$secrets" "$state")
	printf 'POSTGRES_PASSWORD=%s\nPOSTGRES_USER=%s\nPOSTGRES_DB=%s\n' "$password" "$user" "$db" >"$secrets"

	local container
	container=$(docker run --detach --name "$cluster" --label "$LABEL=$id" \
		--env-file "$secrets" --publish 127.0.0.1::5432 \
		--tmpfs /var/lib/postgresql/data:rw,size=512m --tmpfs /var/run/postgresql:rw,size=16m \
		--memory 512m --cpus 1 --pids-limit 256 --security-opt no-new-privileges \
		"$IMAGE" postgres -c cluster_name="$cluster")
	cleanup_container=$container
	rm -f "$secrets"

	local ready=false
	for _ in $(seq 1 60); do
		# The entrypoint's temporary init server listens on the socket only,
		# so a TCP check succeeds once the final server is up.
		if docker exec "$container" pg_isready --quiet -h 127.0.0.1 -U "$user" -d "$db"; then
			ready=true
			break
		fi
		sleep 1
	done
	"$ready" || die "PostgreSQL did not become ready"

	local identity
	identity=$(docker exec "$container" psql -X -A -t -h 127.0.0.1 -U "$user" -d "$db" -c \
		"SELECT current_setting('cluster_name') || '|' || (SELECT count(*) FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace WHERE n.nspname = 'public' AND c.relkind IN ('r', 'p'))")
	[ "$identity" = "$cluster|0" ] || die "unexpected instance identity: $identity"

	if "$fixtures"; then
		local migration
		for migration in "$MIGRATIONS"/*.up.sql; do
			docker exec -i "$container" psql -X -q -v ON_ERROR_STOP=1 -h 127.0.0.1 -U "$user" -d "$db" <"$migration" ||
				die "applying $(basename "$migration") failed (the fixtures need PostGIS)"
		done
	fi

	local port
	port=$(docker port "$container" 5432/tcp | head -n 1)
	port=${port##*:}
	local dsn="postgres://$user:$password@127.0.0.1:$port/$db?sslmode=disable"
	printf 'SQLEXT_TEST_POSTGRES_DSN=%s\nSQLEXT_TEST_POSTGRES_CLUSTER=%s\nSQLEXT_TEST_POSTGRES_CONTAINER=%s\n' \
		"$dsn" "$cluster" "$container" >"$state"

	if [ -n "${GITHUB_ENV:-}" ]; then
		echo "::add-mask::$password"
		printf 'SQLEXT_TEST_POSTGRES_DSN=%s\nSQLEXT_TEST_POSTGRES_CLUSTER=%s\nSQLEXT_TEST_POSTGRES_STATE=%s\n' \
			"$dsn" "$cluster" "$state" >>"$GITHUB_ENV"
	fi
	cleanup_container=""
	cleanup_files=()
	trap - EXIT
	printf 'started %s (cluster_name %s, 127.0.0.1:%s, fixtures: %s)\n' "$container" "$cluster" "$port" "$fixtures" >&2
	printf '%s\n' "$state"
}

down() {
	local state=${1:-}
	[ -n "$state" ] && [ -f "$state" ] || die "usage: $0 down STATE_FILE"
	local container
	container=$(sed -n 's/^SQLEXT_TEST_POSTGRES_CONTAINER=//p' "$state")
	[ -n "$container" ] || die "no container recorded in $state"
	local label
	label=$(docker inspect --format "{{index .Config.Labels \"$LABEL\"}}" "$container") ||
		die "container $container not found"
	[ -n "$label" ] || die "container $container is not a test instance; not removing it"
	docker rm --force "$container" >/dev/null
	rm -f "$state"
	printf 'removed %s\n' "$container" >&2
}

case "${1:-}" in
up)
	shift
	up "$@"
	;;
down)
	shift
	down "$@"
	;;
*) die "usage: $0 up [--fixtures] | down STATE_FILE" ;;
esac
