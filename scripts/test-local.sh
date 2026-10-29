#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
if [ ! -f deploy/.env.test ]; then
    echo 'Créer deploy/.env.test à partir de deploy/test.env.example.' >&2
    exit 1
fi
set -a
. ./deploy/.env.test
set +a
: "${TEST_SLACK_BOT_TOKEN:?Renseigner le token bot de l’application Slack de test dans deploy/.env.test}"
: "${TEST_SLACK_APP_TOKEN:?Renseigner le token app-level Slack de test dans deploy/.env.test}"
: "${TEST_DISCORD_BOT_TOKEN:?Renseigner le token Discord dans deploy/.env.test}"
export MATTERBRIDGE_SLACK_MAIN_TOKEN="$TEST_SLACK_BOT_TOKEN"
export MATTERBRIDGE_SLACK_MAIN_APPTOKEN="$TEST_SLACK_APP_TOKEN"
export MATTERBRIDGE_DISCORD_MAIN_TOKEN="$TEST_DISCORD_BOT_TOKEN"
mkdir -p data/test
go build -p 2 -mod=vendor -o data/test/matterbridge .
cd data/test
exec ./matterbridge -conf ../../deploy/matterbridge.test.toml
