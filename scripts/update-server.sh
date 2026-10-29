#!/usr/bin/env bash
# Load the full function before git pull updates this script on disk.
main() {
    set -eu
    repo_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
    cd "$repo_dir/.."
    test "$(basename "$repo_dir")" = source
    exec 9>.deploy.lock
    flock -n 9 || { echo 'Un déploiement est déjà en cours.' >&2; exit 1; }
    test -s .env
    test -s compose.yaml
    test -s matterbridge.toml
    docker compose version >/dev/null
    source_dir="source"
    test "$(git -C "$source_dir" remote get-url origin)" = 'https://github.com/KeTrevis/matterbridge.git'
    test "$(git -C "$source_dir" branch --show-current)" = master
    test -z "$(git -C "$source_dir" status --porcelain)" || {
        echo 'Le clone serveur contient des modifications locales ; déploiement interrompu.' >&2
        exit 1
    }
    git -C "$source_dir" pull --ff-only origin master
    revision=$(git -C "$source_dir" rev-parse HEAD)
    release="$(date -u +%Y%m%dT%H%M%SZ)-${revision:0:12}"
    backup=".backups/$release"
    umask 077
    mkdir -p "$backup"
    cp -p compose.yaml "$backup/compose.yaml"
    cp -p matterbridge.toml "$backup/matterbridge.toml"
    next=".compose-$release.yaml"
    sed -e "s|context: ..$|context: $source_dir|" \
        -e "s|image: slack-discord-bridge:local$|image: slack-discord-bridge:$release|" \
        -e "s|./reaction-emojis.json:|./$source_dir/deploy/reaction-emojis.json:|" \
        "$source_dir/deploy/compose.yaml" > "$next"
    compose() { docker compose -p slack-discord-bridge --env-file .env -f "$1" "${@:2}"; }
    trap 'rm -f "${next:-}"' EXIT
    echo "Construction de $revision (le bridge actuel continue de tourner)…"
    compose "$next" build bridge

    rollback() {
        trap - ERR HUP INT TERM
        echo "Échec du démarrage : restauration de $backup" >&2
        cp -p "$backup/compose.yaml" compose.yaml
        cp -p "$backup/matterbridge.toml" matterbridge.toml
        compose compose.yaml up -d --no-build --pull never bridge || {
            echo "La restauration a échoué. Sauvegarde : $PWD/$backup" >&2
            exit 1
        }
        exit 1
    }
    set -E
    trap rollback ERR HUP INT TERM
    cp "$source_dir/deploy/matterbridge.toml" matterbridge.toml
    mv "$next" compose.yaml
    compose compose.yaml up -d --no-build --pull never bridge
    container=$(compose compose.yaml ps -q bridge)
    test -n "$container"
    echo 'Vérification du conteneur pendant 20 secondes…'
    for attempt in $(seq 1 10); do
        sleep 2
        state=$(docker inspect --format '{{.State.Running}} {{.RestartCount}}' "$container")
        test "$state" = 'true 0'
    done
    trap - ERR HUP INT TERM
    echo "Déployé : $revision ; image : slack-discord-bridge:$release"
    echo "Sauvegarde : $PWD/$backup"
    compose compose.yaml ps bridge
}
main "$@"
